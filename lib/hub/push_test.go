package hub

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/webpush"
)

// browser stands for a browser that subscribed, and its push service: it
// decrypts what the hub pushes.
type browser struct {
	t       *testing.T
	key     *ecdh.PrivateKey
	auth    []byte
	server  *httptest.Server
	status  int
	pushed  chan pushPayload
	headers chan http.Header
}

func newBrowser(t *testing.T) *browser {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	b := &browser{t: t, key: key, auth: make([]byte, 16), status: http.StatusCreated, pushed: make(chan pushPayload, 10), headers: make(chan http.Header, 10)}
	_, _ = rand.Read(b.auth)
	b.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(b.status)
		if b.status >= 300 {
			return
		}
		plaintext, err := webpush.Decrypt(body, b.key, b.auth)
		if !assert.NoError(t, err) {
			return
		}
		var payload pushPayload
		assert.NoError(t, json.Unmarshal(plaintext, &payload))
		b.headers <- r.Header.Clone()
		b.pushed <- payload
	}))
	t.Cleanup(b.server.Close)
	return b
}

func (b *browser) subscription(path string) webpush.Subscription {
	return webpush.Subscription{
		Endpoint: b.server.URL + path,
		Keys: webpush.Keys{
			P256dh: base64.RawURLEncoding.EncodeToString(b.key.PublicKey().Bytes()),
			Auth:   base64.RawURLEncoding.EncodeToString(b.auth),
		},
	}
}

func (b *browser) next() pushPayload {
	b.t.Helper()
	select {
	case payload := <-b.pushed:
		return payload
	case <-time.After(5 * time.Second):
		b.t.Fatal("nothing pushed")
		return pushPayload{}
	}
}

func (b *browser) none() {
	b.t.Helper()
	select {
	case payload := <-b.pushed:
		b.t.Fatalf("pushed %+v", payload)
	case <-time.After(100 * time.Millisecond):
	}
}

func pushRequest(t *testing.T, srv *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestPushRoutes(t *testing.T) {
	b := newBrowser(t)
	state := filepath.Join(t.TempDir(), "instances.json")
	h, err := New(Config{Token: "t", Secret: "s", StatePath: state, PushClient: b.server.Client()})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	code, body := pushRequest(t, srv, http.MethodGet, "/api/push", nil)
	require.Equal(t, http.StatusOK, code)
	var info pushInfo
	require.NoError(t, json.Unmarshal(body, &info))
	assert.Equal(t, webpush.PublicKey(h.push.devices.key), info.PublicKey)
	stat, err := os.Stat(filepath.Join(filepath.Dir(state), "push.json"))
	require.NoError(t, err, "the key is kept at once")
	assert.Equal(t, os.FileMode(0o600), stat.Mode().Perm())

	bad := b.subscription("/x")
	bad.Endpoint = "http://push.example.net/x"
	code, _ = pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: bad})
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = pushRequest(t, srv, http.MethodPost, "/api/push/test", pushEndpointBody{Endpoint: b.server.URL + "/a"})
	assert.Equal(t, http.StatusNotFound, code)

	sub := b.subscription("/a")
	code, _ = pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub, Label: "Pixel ·   Chrome", Finished: true})
	require.Equal(t, http.StatusOK, code)
	code, body = pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub})
	require.Equal(t, http.StatusOK, code)
	var device pushDevice
	require.NoError(t, json.Unmarshal(body, &device))
	assert.Equal(t, pushDeviceID(sub.Endpoint), device.ID)
	assert.Equal(t, "Pixel · Chrome", device.Label, "a renewal without a label keeps it")
	assert.True(t, device.Finished, "a renewal keeps the settings")
	assert.Nil(t, device.ExpiresAt, "renewed with the hub token: no session to end")
	subs := listed(t, h)
	require.Len(t, subs, 1, "the same endpoint again renews it")

	code, _ = pushRequest(t, srv, http.MethodPost, "/api/push/test", pushEndpointBody{Endpoint: sub.Endpoint})
	require.Equal(t, http.StatusNoContent, code)
	payload := b.next()
	assert.Equal(t, pushTest, payload.Kind)
	assert.Equal(t, "Tendyard Hub", payload.Title)

	// A restarted hub keeps its key and the subscriptions.
	again, err := New(Config{Token: "t", Secret: "s", StatePath: state})
	require.NoError(t, err)
	assert.True(t, h.push.devices.key.Equal(again.push.devices.key))
	require.Len(t, listed(t, again), 1)
	assert.Equal(t, sub, listed(t, again)[0].Subscription)
	assert.True(t, listed(t, again)[0].Finished)
	assert.False(t, listed(t, again)[0].LastSentAt.IsZero(), "the test push was recorded")

	// A subscription its push service ended is forgotten.
	b.status = http.StatusGone
	code, _ = pushRequest(t, srv, http.MethodPost, "/api/push/test", pushEndpointBody{Endpoint: sub.Endpoint})
	assert.Equal(t, http.StatusGone, code)
	assert.Empty(t, listed(t, h))

	b.status = http.StatusCreated
	code, _ = pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub})
	require.Equal(t, http.StatusOK, code)
	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/push/subscription", pushEndpointBody{Endpoint: sub.Endpoint})
	require.Equal(t, http.StatusNoContent, code)
	assert.Empty(t, listed(t, h))
	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/push/subscription", pushEndpointBody{})
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestPushRoutesNeedSignIn(t *testing.T) {
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/push")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestPushStoreKeepsTheNewest(t *testing.T) {
	s, err := newPushStore("", "")
	require.NoError(t, err)
	for i := range maxPushSubscriptions + 2 {
		_, err = s.renew(pushSubscription{Subscription: webpush.Subscription{Endpoint: "https://push.example/" + string(rune('a'+i))}})
		require.NoError(t, err)
	}
	subs, err := s.list(time.Now())
	require.NoError(t, err)
	require.Len(t, subs, maxPushSubscriptions)
	assert.Equal(t, "https://push.example/c", subs[0].Endpoint, "the two oldest went")
}

// pushHub is a hub that runs its notifier on a mock clock, with b
// subscribed.
func pushHub(t *testing.T, b *browser, finished bool) (*Hub, *quartz.Mock) {
	clock := quartz.NewMock(t)
	h, err := New(Config{Token: "t", Secret: "s", Clock: clock, PushClient: b.server.Client()})
	require.NoError(t, err)
	_, err = h.push.devices.renew(pushSubscription{Subscription: b.subscription("/p"), PushPrefs: PushPrefs{Finished: finished}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.notifyPush(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	// The notifier sees where the instances stand once it subscribed.
	require.Eventually(t, func() bool {
		h.store.mu.Lock()
		defer h.store.mu.Unlock()
		return len(h.store.subs) > 0
	}, 5*time.Second, time.Millisecond)
	return h, clock
}

func setSummary(h *Hub, summary instanceapi.SummaryBody) {
	_, _ = h.store.update("dev:1", true, func(i *Instance) {
		i.Name = "dev"
		i.URL = "http://dev:1"
		i.Connection = instanceapi.ConnectionConnected
		i.Summary = &summary
	})
}

func approval(id, title string) instanceapi.SummaryBody {
	return instanceapi.SummaryBody{Attention: instanceapi.AttentionRequest, Request: &instanceapi.SummaryRequest{ID: id, Title: title}}
}

// pushAfterDelay makes change, waits for the notifier to schedule its
// push, and moves the clock past pushDelay.
func pushAfterDelay(t *testing.T, clock *quartz.Mock, change func()) {
	t.Helper()
	ctx := context.Background()
	trap := clock.Trap().AfterFunc("hub", "push")
	defer trap.Close()
	change()
	trap.MustWait(ctx).Release()
	clock.Advance(pushDelay).MustWait(ctx)
}

func TestPushNeedsYou(t *testing.T) {
	b := newBrowser(t)
	h, clock := pushHub(t, b, false)

	// Where it stands when the notifier first sees it isn't news.
	setSummary(h, approval("r0", "rm -rf /tmp/x"))
	b.none()
	setSummary(h, instanceapi.SummaryBody{Attention: instanceapi.AttentionNone})
	b.none()

	pushAfterDelay(t, clock, func() { setSummary(h, approval("r1", "go   test\n./...")) })
	payload := b.next()
	assert.Equal(t, pushPayload{Kind: pushNeedsYou, Title: "dev needs you", Body: "go test ./...", ID: "dev:1", Tag: "agentapi-hub-dev:1", Badge: 1}, payload)
	headers := <-b.headers
	assert.Equal(t, "high", headers.Get("Urgency"))
	assert.Equal(t, pushTopic("agentapi-hub-dev:1"), headers.Get("Topic"))
	assert.Len(t, headers.Get("Topic"), 32)

	// The same request again, as a reconnect reads it: nothing new.
	setSummary(h, approval("r1", "go test ./..."))
	b.none()

	// Answered before the delay: nothing.
	pushAfterDelay(t, clock, func() {
		setSummary(h, approval("r2", "ls"))
		setSummary(h, instanceapi.SummaryBody{Attention: instanceapi.AttentionNone})
	})
	b.none()

	// Muted before the delay: nothing.
	pushAfterDelay(t, clock, func() {
		setSummary(h, instanceapi.SummaryBody{Attention: instanceapi.AttentionLogin})
		_, _ = h.store.update("dev:1", false, func(i *Instance) { i.Muted = "login" })
	})
	b.none()

	pushAfterDelay(t, clock, func() {
		setSummary(h, instanceapi.SummaryBody{Attention: instanceapi.AttentionLimit, LimitStop: nil})
	})
	assert.Equal(t, "The account reached its limit", b.next().Body)
}

func TestPushFinished(t *testing.T) {
	ended := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	running := instanceapi.SummaryBody{Task: &instanceapi.Task{ID: 4, Prompt: "fix the build"}}
	done := instanceapi.SummaryBody{Title: "Fix the build", Unread: true, Task: &instanceapi.Task{ID: 4, Prompt: "fix the build", EndedAt: &ended}}

	t.Run("pushes to whoever wants it", func(t *testing.T) {
		b := newBrowser(t)
		h, clock := pushHub(t, b, true)
		setSummary(h, running)
		pushAfterDelay(t, clock, func() { setSummary(h, done) })
		payload := b.next()
		assert.Equal(t, pushPayload{Kind: pushFinished, Title: "dev finished", Body: "Fix the build", ID: "dev:1", Tag: "agentapi-hub-dev:1", Badge: 0}, payload)
		assert.Equal(t, "normal", (<-b.headers).Get("Urgency"))
	})

	t.Run("not when read before the delay", func(t *testing.T) {
		b := newBrowser(t)
		h, clock := pushHub(t, b, true)
		setSummary(h, running)
		pushAfterDelay(t, clock, func() {
			setSummary(h, done)
			read := done
			read.Unread = false
			setSummary(h, read)
		})
		b.none()
	})

	t.Run("not to a browser that wants only what needs it", func(t *testing.T) {
		b := newBrowser(t)
		h, clock := pushHub(t, b, false)
		setSummary(h, running)
		pushAfterDelay(t, clock, func() { setSummary(h, done) })
		b.none()
	})

	t.Run("not for a task it never saw run", func(t *testing.T) {
		b := newBrowser(t)
		h, _ := pushHub(t, b, true)
		setSummary(h, instanceapi.SummaryBody{})
		setSummary(h, done)
		b.none()
	})
}

func TestPushForClips(t *testing.T) {
	long := make([]rune, 300)
	for i := range long {
		long[i] = '字'
	}
	i := Instance{Connection: instanceapi.ConnectionConnected, State: instanceapi.StateNeedsYou, Summary: &instanceapi.SummaryBody{Attention: instanceapi.AttentionRequest, Request: &instanceapi.SummaryRequest{ID: "r", Title: string(long)}}}
	i.ID = "x"
	payload, ok := pushFor(i, pushNeedsYou, pushSeen{attention: "request:r"})
	require.True(t, ok)
	assert.Equal(t, "x needs you", payload.Title)
	assert.Len(t, []rune(payload.Body), maxPushText)
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.Less(t, len(data), webpush.MaxPayload)
}

func listed(t *testing.T, h *Hub) []pushSubscription {
	t.Helper()
	subs, err := h.push.devices.list(h.clock.Now())
	require.NoError(t, err)
	return subs
}
