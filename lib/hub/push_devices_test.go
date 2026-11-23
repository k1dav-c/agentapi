package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/dispatch"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/webpush"
)

// A device subscribed from a signed-in page stays as long as that page's
// session: past it, the hub forgets it.
func TestPushDeviceEndsWithItsSession(t *testing.T) {
	b := newBrowser(t)
	clock := quartz.NewMock(t)
	start := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	clock.Set(start)
	h, err := New(Config{Token: "t", Secret: "s", Clock: clock, PushClient: b.server.Client()})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	signIn, err := http.Post(srv.URL+"/api/session", "application/json", jsonBody(t, signInBody{Token: "t"}))
	require.NoError(t, err)
	_ = signIn.Body.Close()
	cookie := signIn.Cookies()[0]

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/push/subscription", jsonBody(t, pushSubscribeBody{Subscription: b.subscription("/s")}))
	require.NoError(t, err)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var device pushDevice
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&device))
	_ = resp.Body.Close()
	require.NotNil(t, device.ExpiresAt)
	assert.True(t, device.ExpiresAt.Equal(start.Add(sessionMaxAge)))

	clock.Set(start.Add(sessionMaxAge - time.Minute))
	assert.Len(t, listed(t, h), 1)
	clock.Set(start.Add(sessionMaxAge))
	assert.Empty(t, listed(t, h), "its session ended")
	code, _ := pushRequest(t, srv, http.MethodPost, "/api/push/test", pushEndpointBody{Endpoint: b.server.URL + "/s"})
	assert.Equal(t, http.StatusNotFound, code)
}

// A hub started with another token forgets the devices of the one before.
func TestPushDevicesEndWithTheHubToken(t *testing.T) {
	b := newBrowser(t)
	state := filepath.Join(t.TempDir(), "instances.json")
	h, err := New(Config{Token: "t", Secret: "s", StatePath: state})
	require.NoError(t, err)
	_, err = h.push.devices.renew(pushSubscription{Subscription: b.subscription("/a"), TokenID: pushTokenID("t")}, true)
	require.NoError(t, err)
	require.NoError(t, h.Close())

	same, err := New(Config{Token: "t", Secret: "s", StatePath: state})
	require.NoError(t, err)
	assert.Len(t, listed(t, same), 1)
	require.NoError(t, same.Close())
	other, err := New(Config{Token: "u", Secret: "s", StatePath: state})
	require.NoError(t, err)
	assert.Empty(t, listed(t, other))
	require.NoError(t, other.Close())
	assert.True(t, h.push.devices.key.Equal(other.push.devices.key), "the key stays")
	again, err := New(Config{Token: "t", Secret: "s", StatePath: state})
	require.NoError(t, err)
	assert.Empty(t, listed(t, again), "forgotten for good")
}

func TestPushDeviceRoutes(t *testing.T) {
	b := newBrowser(t)
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	code, _ := pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: b.subscription("/a"), Label: "iPhone · Home Screen"})
	require.Equal(t, http.StatusOK, code)
	id := pushDeviceID(b.server.URL + "/a")

	code, body := pushRequest(t, srv, http.MethodGet, "/api/push/devices", nil)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, string(body), "/a\"", "no endpoint")
	assert.NotContains(t, string(body), "p256dh")
	var devices pushDevicesBody
	require.NoError(t, json.Unmarshal(body, &devices))
	require.Len(t, devices.Devices, 1)
	assert.Equal(t, id, devices.Devices[0].ID)
	assert.Equal(t, "iPhone · Home Screen", devices.Devices[0].Label)
	assert.Equal(t, []string{}, devices.Devices[0].Groups)

	for _, bad := range []string{
		`{"quiet_hours": {"start": "22:00", "end": "22:00", "time_zone": "Asia/Taipei"}}`,
		`{"quiet_hours": {"start": "25:00", "end": "08:00", "time_zone": "Asia/Taipei"}}`,
		`{"quiet_hours": {"start": "22:00", "end": "08:00", "time_zone": "Mars/Olympus"}}`,
		`{"groups": [" padded "]}`,
		`not json`,
	} {
		code, _ = rawRequest(t, srv, http.MethodPatch, "/api/push/devices/"+id, bad)
		assert.Equal(t, http.StatusBadRequest, code, bad)
	}
	code, body = rawRequest(t, srv, http.MethodPatch, "/api/push/devices/"+id, `{"issues": true, "groups": ["Client work", "", "Client work"], "pinned": true, "quiet_hours": {"start": "22:00", "end": "08:00", "time_zone": "Asia/Taipei"}}`)
	require.Equal(t, http.StatusOK, code)
	var device pushDevice
	require.NoError(t, json.Unmarshal(body, &device))
	assert.True(t, device.Issues)
	assert.False(t, device.Finished, "left as it was")
	assert.Equal(t, []string{"", "Client work"}, device.Groups)
	assert.Equal(t, &quietHours{Start: "22:00", End: "08:00", TimeZone: "Asia/Taipei"}, device.QuietHours)
	code, body = rawRequest(t, srv, http.MethodPatch, "/api/push/devices/"+id, `{"quiet_hours": {}, "groups": []}`)
	require.Equal(t, http.StatusOK, code)
	device = pushDevice{}
	require.NoError(t, json.Unmarshal(body, &device))
	assert.Nil(t, device.QuietHours)
	assert.Empty(t, device.Groups)
	assert.True(t, device.Issues)

	code, _ = rawRequest(t, srv, http.MethodPatch, "/api/push/devices/nope", `{"issues": true}`)
	assert.Equal(t, http.StatusNotFound, code)
	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/push/devices/"+id, nil)
	assert.Equal(t, http.StatusNoContent, code)
	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/push/devices/"+id, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestQuietHours(t *testing.T) {
	taipei, err := time.LoadLocation("Asia/Taipei")
	require.NoError(t, err)
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 23, hour, minute, 0, 0, taipei).UTC() }
	night := &quietHours{Start: "22:00", End: "08:00", TimeZone: "Asia/Taipei"}
	assert.True(t, night.quiet(at(22, 0)))
	assert.True(t, night.quiet(at(3, 0)))
	assert.True(t, night.quiet(at(7, 59)))
	assert.False(t, night.quiet(at(8, 0)))
	assert.False(t, night.quiet(at(21, 59)))
	lunch := &quietHours{Start: "12:00", End: "13:00", TimeZone: "Asia/Taipei"}
	assert.True(t, lunch.quiet(at(12, 30)))
	assert.False(t, lunch.quiet(at(13, 0)))
	assert.False(t, (*quietHours)(nil).quiet(at(3, 0)))
}

func TestPushPrefsWants(t *testing.T) {
	now := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC) // 10:00 in Taipei
	pinned := Instance{Record: Record{Pin: 1}}
	grouped := Instance{Record: Record{Group: "Client work"}}
	ungrouped := Instance{}
	needs := pushPayload{Kind: pushNeedsYou}
	groupsNow := map[string]bool{"": true, "Client work": true}
	every := PushPrefs{}
	assert.True(t, every.wants(needs, &ungrouped, now, groupsNow))
	assert.False(t, every.wants(pushPayload{Kind: pushFinished}, &ungrouped, now, groupsNow))
	assert.False(t, every.wants(pushPayload{Kind: pushIssue}, nil, now, groupsNow))
	assert.True(t, PushPrefs{Finished: true}.wants(pushPayload{Kind: pushFinished}, &ungrouped, now, groupsNow))
	assert.True(t, PushPrefs{Issues: true}.wants(pushPayload{Kind: pushIssue}, nil, now, groupsNow))

	groups := PushPrefs{Groups: []string{"Client work"}}
	assert.True(t, groups.wants(needs, &grouped, now, groupsNow))
	assert.False(t, groups.wants(needs, &ungrouped, now, groupsNow))
	assert.False(t, groups.wants(needs, &pinned, now, groupsNow))
	onlyPinned := PushPrefs{Pinned: true}
	assert.True(t, onlyPinned.wants(needs, &pinned, now, groupsNow))
	assert.False(t, onlyPinned.wants(needs, &grouped, now, groupsNow))
	assert.True(t, PushPrefs{Groups: []string{""}}.wants(needs, &ungrouped, now, groupsNow), `"" is the ungrouped ones`)
	assert.True(t, PushPrefs{Groups: []string{"Other"}, Issues: true}.wants(pushPayload{Kind: pushIssue}, nil, now, groupsNow), "issues aren't picked by group")

	// A group no agent is in any more (renamed, or emptied) counts for
	// nothing: following only such groups is following every agent.
	gone := PushPrefs{Groups: []string{"Renamed"}}
	assert.True(t, gone.wants(needs, &grouped, now, groupsNow))
	assert.True(t, PushPrefs{Groups: []string{"Renamed", "Client work"}}.wants(needs, &grouped, now, groupsNow))
	assert.False(t, PushPrefs{Groups: []string{"Renamed", "Client work"}}.wants(needs, &ungrouped, now, groupsNow))
	assert.False(t, PushPrefs{Groups: []string{"Renamed"}, Pinned: true}.wants(needs, &grouped, now, groupsNow), "still follows the pinned ones")

	quiet := PushPrefs{QuietHours: &quietHours{Start: "09:00", End: "11:00", TimeZone: "Asia/Taipei"}}
	assert.False(t, quiet.wants(needs, &ungrouped, now, groupsNow))
	assert.True(t, quiet.wants(pushPayload{Kind: pushTest}, nil, now, groupsNow), "a test goes anyway")
}

// The hub pushes each device what it wants: a device that follows one
// group doesn't get another's agent.
func TestPushFollowsTheDevicesGroups(t *testing.T) {
	b := newBrowser(t)
	h, clock := pushHub(t, b, false)
	_, ok := h.push.devices.update(pushDeviceID(b.server.URL+"/p"), func(sub *pushSubscription) { sub.Groups = []string{"Client work"} })
	require.True(t, ok)

	// Another agent is in the group, so it is a group of the hub's.
	_, _ = h.store.update("other:1", true, func(i *Instance) { i.Group = "Client work" })
	setSummary(h, instanceapi.SummaryBody{})
	pushAfterDelay(t, clock, func() { setSummary(h, approval("r1", "ls")) })
	b.none()
	_, _ = h.store.update("dev:1", false, func(i *Instance) { i.Group = "Client work" })
	pushAfterDelay(t, clock, func() { setSummary(h, approval("r2", "ls -la")) })
	assert.Equal(t, "ls -la", b.next().Body)
}

func TestIssuePush(t *testing.T) {
	job := dispatch.Job{Key: "acme~widget~12", Repo: "acme/widget", Issue: 12, Title: "Fix   the build"}
	working := jobSeen{state: dispatch.JobWorking}
	for _, tt := range []struct {
		name   string
		before jobSeen
		now    jobSeen
		error  string
		title  string
		body   string
	}{
		{"pull request", working, jobSeen{state: dispatch.JobReview, pr: "https://github.com/acme/widget/pull/13"}, "", "acme/widget#12: pull request opened", "Fix the build"},
		{"failed", working, jobSeen{state: dispatch.JobFailed}, "the agent exited", "acme/widget#12 failed", "the agent exited"},
		{"done", jobSeen{state: dispatch.JobReview, pr: "x"}, jobSeen{state: dispatch.JobDone, pr: "x"}, "", "acme/widget#12 done", "Fix the build"},
		{"nothing new", working, working, "", "", ""},
		{"still failed", jobSeen{state: dispatch.JobFailed}, jobSeen{state: dispatch.JobFailed}, "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			job := job
			job.Error = tt.error
			payload, ok := issuePush(job, tt.before, tt.now)
			if tt.title == "" {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.Equal(t, pushPayload{Kind: pushIssue, Title: tt.title, Body: tt.body, Issue: job.Key, Tag: "agentapi-hub-issue-" + job.Key}, payload)
		})
	}
}

func jsonBody(t *testing.T, v any) *strings.Reader {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return strings.NewReader(string(data))
}

func rawRequest(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// The test servers are at 127.0.0.1: the hub pushes there in this
// package's tests.
func init() {
	pushServiceHosts = append(pushServiceHosts, "127.0.0.1")
}

func TestCheckPushService(t *testing.T) {
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://web.push.apple.com/QGx",
		"https://updates.push.services.mozilla.com/wpush/v2/x",
		"https://wns2-par02p.notify.windows.com/w/?token=x",
	} {
		assert.NoError(t, checkPushService(ok, pushServiceHosts), ok)
	}
	for _, bad := range []string{
		"https://example.com/push",
		"https://evilgoogleapis.com/x",
		"https://googleapis.com.evil.example/x",
		"https://10.0.0.1/x",
		"https://localhost:8443/x",
		"https://127.0.0.1/x",
	} {
		assert.ErrorIs(t, checkPushService(bad, pushServiceHosts[:4]), errUnknownPushService, bad)
	}
	// --push-service-hosts: a name, or .domain for its subdomains.
	assert.NoError(t, checkPushService("https://push.example.org/x", []string{"push.example.org"}))
	assert.Error(t, checkPushService("https://a.push.example.org/x", []string{"push.example.org"}))
	assert.NoError(t, checkPushService("https://a.push.example.org/x", []string{".push.example.org"}))
	assert.NoError(t, checkPushService("https://push.example.org/x", []string{".push.example.org"}))
}

func TestPushRefusesOtherEndpoints(t *testing.T) {
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	sub := newBrowser(t).subscription("/x")
	sub.Endpoint = "https://intranet.example/admin"
	code, body := pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, string(body), "push service")
	assert.Empty(t, listed(t, h))
}

func TestPushClientFollowsNoRedirect(t *testing.T) {
	assert.ErrorIs(t, newPushClient().CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

// A hub of 5.8 kept its devices without a token: the next one adopts them
// with its token, and keeps their settings.
func TestPushDevicesOf58Stay(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "instances.json")
	key, err := webpush.GenerateKey()
	require.NoError(t, err)
	encoded, err := webpush.EncodePrivateKey(key)
	require.NoError(t, err)
	sub := newBrowser(t).subscription("/old")
	data, err := json.Marshal(map[string]any{"vapid_private_key": encoded, "subscriptions": []any{map[string]any{
		"endpoint": sub.Endpoint, "keys": sub.Keys, "finished": true, "created_at": "2026-11-23T09:30:00Z",
	}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "push.json"), data, 0o600))

	h, err := New(Config{Token: "t", Secret: "s", StatePath: state})
	require.NoError(t, err)
	subs := listed(t, h)
	require.Len(t, subs, 1)
	assert.True(t, subs[0].Finished)
	assert.Equal(t, pushTokenID("t"), subs[0].TokenID)
	require.NoError(t, h.Close())
	again, err := New(Config{Token: "t", Secret: "s", StatePath: state})
	require.NoError(t, err)
	assert.Len(t, listed(t, again), 1)
}

// A device removed from another device's settings stays removed: its page
// renewing it is 410, until it turns push on again.
func TestPushRemovedDeviceStaysRemoved(t *testing.T) {
	b := newBrowser(t)
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	sub := b.subscription("/phone")
	code, _ := pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub, Enable: true})
	require.Equal(t, http.StatusOK, code)
	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/push/devices/"+pushDeviceID(sub.Endpoint), nil)
	require.Equal(t, http.StatusNoContent, code)

	code, body := pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub})
	assert.Equal(t, http.StatusGone, code, string(body))
	assert.Empty(t, listed(t, h))
	code, _ = pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub, Enable: true})
	assert.Equal(t, http.StatusOK, code, "turned on again there")
	code, _ = pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub})
	assert.Equal(t, http.StatusOK, code, "and renewed")

	// Its own page turning push off isn't a removal: it may come back.
	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/push/subscription", pushEndpointBody{Endpoint: sub.Endpoint})
	require.Equal(t, http.StatusNoContent, code)
	code, _ = pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub})
	assert.Equal(t, http.StatusOK, code)
}

// Signing out with the browser's endpoint stops the pushes to it on the
// hub, whatever the browser's own unsubscribe does.
func TestSignOutStopsThePushes(t *testing.T) {
	b := newBrowser(t)
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	sub := b.subscription("/s")
	code, _ := pushRequest(t, srv, http.MethodPut, "/api/push/subscription", pushSubscribeBody{Subscription: sub})
	require.Equal(t, http.StatusOK, code)

	// Not signed in: the endpoint alone stops nothing.
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/session", jsonBody(t, signOutBody{Endpoint: sub.Endpoint}))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Len(t, listed(t, h), 1)

	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/session", signOutBody{Endpoint: sub.Endpoint})
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, listed(t, h))
	code, _ = pushRequest(t, srv, http.MethodDelete, "/api/session", nil)
	assert.Equal(t, http.StatusOK, code, "without a body, as before")
}

// Past the most devices, those whose session ended go before the oldest.
func TestPushStoreDropsTheExpiredFirst(t *testing.T) {
	s, err := newPushStore("", "", nil)
	require.NoError(t, err)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for i := range maxPushSubscriptions {
		sub := pushSubscription{Subscription: webpush.Subscription{Endpoint: fmt.Sprintf("https://push.example/%d", i)}}
		if i == 10 {
			sub.ExpiresAt = now.Add(-time.Minute)
		}
		_, err := s.renew(sub, true)
		require.NoError(t, err)
	}
	_, err = s.renew(pushSubscription{Subscription: webpush.Subscription{Endpoint: "https://push.example/new"}, RenewedAt: now}, true)
	require.NoError(t, err)
	subs := s.list(now)
	require.Len(t, subs, maxPushSubscriptions)
	assert.Equal(t, "https://push.example/0", subs[0].Endpoint, "the oldest stays")
	assert.False(t, slices.ContainsFunc(subs, byEndpoint("https://push.example/10")))
}

// stop waits for the pushes under way, and starts none after.
func TestPusherStopWaits(t *testing.T) {
	release := make(chan struct{})
	served := make(chan struct{}, 4)
	service := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served <- struct{}{}
		<-release
		w.WriteHeader(http.StatusCreated)
	}))
	defer service.Close()
	h, err := New(Config{Token: "t", Secret: "s", PushClient: service.Client()})
	require.NoError(t, err)
	b := newBrowser(t)
	sub := b.subscription("/x")
	sub.Endpoint = service.URL + "/x"
	_, err = h.push.devices.renew(pushSubscription{Subscription: sub}, true)
	require.NoError(t, err)

	h.push.goSendAll(context.Background(), pushPayload{Kind: pushNeedsYou, Tag: "t"}, &Instance{})
	<-served
	stopped := make(chan struct{})
	go func() {
		h.push.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop didn't wait for the push under way")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-stopped
	h.push.goSendAll(context.Background(), pushPayload{Kind: pushNeedsYou, Tag: "t"}, &Instance{})
	select {
	case <-served:
		t.Fatal("pushed after stop")
	case <-time.After(100 * time.Millisecond):
	}
}
