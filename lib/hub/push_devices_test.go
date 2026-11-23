package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/dispatch"
	"github.com/coder/agentapi/lib/instanceapi"
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
	_, err = h.push.renew(pushSubscription{Subscription: b.subscription("/a"), TokenID: pushTokenID("t")})
	require.NoError(t, err)

	same, err := New(Config{Token: "t", Secret: "s", StatePath: state})
	require.NoError(t, err)
	assert.Len(t, listed(t, same), 1)
	other, err := New(Config{Token: "u", Secret: "s", StatePath: state})
	require.NoError(t, err)
	assert.Empty(t, listed(t, other))
	assert.True(t, h.push.key.Equal(other.push.key), "the key stays")
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
	every := PushPrefs{}
	assert.True(t, every.wants(needs, &ungrouped, now))
	assert.False(t, every.wants(pushPayload{Kind: pushFinished}, &ungrouped, now))
	assert.False(t, every.wants(pushPayload{Kind: pushIssue}, nil, now))
	assert.True(t, PushPrefs{Finished: true}.wants(pushPayload{Kind: pushFinished}, &ungrouped, now))
	assert.True(t, PushPrefs{Issues: true}.wants(pushPayload{Kind: pushIssue}, nil, now))

	groups := PushPrefs{Groups: []string{"Client work"}}
	assert.True(t, groups.wants(needs, &grouped, now))
	assert.False(t, groups.wants(needs, &ungrouped, now))
	assert.False(t, groups.wants(needs, &pinned, now))
	onlyPinned := PushPrefs{Pinned: true}
	assert.True(t, onlyPinned.wants(needs, &pinned, now))
	assert.False(t, onlyPinned.wants(needs, &grouped, now))
	assert.True(t, PushPrefs{Groups: []string{""}}.wants(needs, &ungrouped, now), `"" is the ungrouped ones`)
	assert.True(t, PushPrefs{Groups: []string{"Other"}, Issues: true}.wants(pushPayload{Kind: pushIssue}, nil, now), "issues aren't picked by group")

	quiet := PushPrefs{QuietHours: &quietHours{Start: "09:00", End: "11:00", TimeZone: "Asia/Taipei"}}
	assert.False(t, quiet.wants(needs, &ungrouped, now))
	assert.True(t, quiet.wants(pushPayload{Kind: pushTest}, nil, now), "a test goes anyway")
}

// The hub pushes each device what it wants: a device that follows one
// group doesn't get another's agent.
func TestPushFollowsTheDevicesGroups(t *testing.T) {
	b := newBrowser(t)
	h, clock := pushHub(t, b, false)
	_, _, err := h.push.update(pushDeviceID(b.server.URL+"/p"), func(sub *pushSubscription) { sub.Groups = []string{"Client work"} })
	require.NoError(t, err)

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
