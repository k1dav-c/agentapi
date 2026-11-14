package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A repository is as recent as its last push or change.
func TestRepoLatest(t *testing.T) {
	pushed := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, pushed, Repo{PushedAt: pushed, UpdatedAt: pushed.Add(-time.Hour)}.Latest())
	assert.Equal(t, pushed.Add(time.Hour), Repo{PushedAt: pushed, UpdatedAt: pushed.Add(time.Hour)}.Latest())
	assert.True(t, Repo{}.Latest().IsZero())
}

// fakeAPI is GitHub's API, as a test sets it up: handle answers each call,
// which calls records as "METHOD path".
type fakeAPI struct {
	mu     sync.Mutex
	calls  []string
	handle func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeAPI) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
}

func (f *fakeAPI) taken() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

// newTestClient returns a client of a fake API at the time *now, and an
// app with a key of its own.
func newTestClient(t *testing.T, f *fakeAPI, now *time.Time) (*Client, *App) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		f.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient()
	c.API, c.HTTP, c.Now = srv.URL, srv.Client(), func() time.Time { return *now }
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	return c, &App{ID: 7, Slug: "agentapi-hub-x", PEM: pemText}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// RepoToken keeps a repository's token until five minutes before it
// expires, and the repository's installation until GitHub says it is gone.
func TestRepoToken(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	installation := 11
	issued := 0
	f := &fakeAPI{}
	f.handle = func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "), "the app's JWT")
		switch {
		case r.URL.Path == "/repos/acme/widget/installation":
			writeJSON(w, http.StatusOK, map[string]any{"id": installation})
		case r.URL.Path == fmt.Sprintf("/app/installations/%d/access_tokens", installation):
			var body struct {
				Repositories []string          `json:"repositories"`
				Permissions  map[string]string `json:"permissions"`
			}
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, []string{"widget"}, body.Repositories, "the repository alone")
			assert.Equal(t, Permissions, body.Permissions)
			issued++
			writeJSON(w, http.StatusCreated, map[string]any{"token": "ghs_" + strconv.Itoa(issued), "expires_at": now.Add(time.Hour)})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
	}
	c, app := newTestClient(t, f, &now)

	token, err := c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.Equal(t, "ghs_1", token.Value)
	assert.Equal(t, []string{"GET /repos/acme/widget/installation", "POST /app/installations/11/access_tokens"}, f.taken())

	// Kept until five minutes before it expires.
	now = now.Add(54 * time.Minute)
	token, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.Equal(t, "ghs_1", token.Value)
	assert.Empty(t, f.taken())
	now = now.Add(time.Minute)
	token, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.Equal(t, "ghs_2", token.Value)
	assert.Equal(t, []string{"POST /app/installations/11/access_tokens"}, f.taken(), "the installation is kept")

	// The app was installed again: the kept installation answers 404, and
	// is looked up again on the next call.
	installation = 12
	now = now.Add(time.Hour)
	_, err = c.RepoToken(ctx, app, "acme/widget")
	assert.True(t, IsStatus(err, http.StatusNotFound))
	assert.Equal(t, []string{"POST /app/installations/11/access_tokens"}, f.taken())
	token, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.Equal(t, "ghs_3", token.Value)
	assert.Equal(t, []string{"GET /repos/acme/widget/installation", "POST /app/installations/12/access_tokens"}, f.taken())

	// Forget drops what is kept.
	c.Forget()
	_, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.Equal(t, []string{"GET /repos/acme/widget/installation", "POST /app/installations/12/access_tokens"}, f.taken())

	// A repository the app isn't on.
	_, err = c.RepoToken(ctx, app, "acme/other")
	assert.True(t, IsStatus(err, http.StatusNotFound))
	assert.ErrorContains(t, err, "Not Found")
}

// InstallationToken is a token for all of an installation's repositories,
// to list them.
func TestInstallationToken(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	f := &fakeAPI{}
	f.handle = func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]any{"permissions": map[string]any{"metadata": "read"}}, body, "no repositories: all of them")
		writeJSON(w, http.StatusCreated, map[string]any{"token": "ghs_all", "expires_at": now.Add(time.Hour)})
	}
	c, app := newTestClient(t, f, &now)
	token, err := c.InstallationToken(context.Background(), app, 11)
	require.NoError(t, err)
	assert.Equal(t, Token{Value: "ghs_all", ExpiresAt: now.Add(time.Hour)}, token)
	assert.Equal(t, []string{"POST /app/installations/11/access_tokens"}, f.taken())
}

// Issues and Repos read page after page until one isn't full, at most
// maxPages; Issues leaves out pull requests.
func TestPagination(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	total := 250
	f := &fakeAPI{}
	f.handle = func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "token ghs_x", r.Header.Get("Authorization"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		assert.Equal(t, "100", r.URL.Query().Get("per_page"))
		var issues []Issue
		var repos []Repo
		for n := (page-1)*100 + 1; n <= min(page*100, total); n++ {
			issue := Issue{Number: n}
			if n%50 == 0 {
				issue.PullRequest = &PullRequestRef{}
			}
			issues = append(issues, issue)
			repos = append(repos, Repo{FullName: fmt.Sprintf("acme/r%d", n)})
		}
		if strings.HasPrefix(r.URL.Path, "/installation/repositories") {
			writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
			return
		}
		writeJSON(w, http.StatusOK, issues)
	}
	c, _ := newTestClient(t, f, &now)
	as := c.With(Token{Value: "ghs_x"})

	issues, err := as.Issues(context.Background(), "acme/widget")
	require.NoError(t, err)
	assert.Len(t, issues, 245, "250 less 5 pull requests")
	assert.Equal(t, 1, issues[0].Number)
	assert.Len(t, f.taken(), 3)

	repos, err := as.Repos(context.Background())
	require.NoError(t, err)
	assert.Len(t, repos, 250)
	assert.Len(t, f.taken(), 3)

	// A full last page is followed by an empty one.
	total = 200
	repos, err = as.Repos(context.Background())
	require.NoError(t, err)
	assert.Len(t, repos, 200)
	assert.Len(t, f.taken(), 3)

	// No more than maxPages.
	total = 5000
	repos, err = as.Repos(context.Background())
	require.NoError(t, err)
	assert.Len(t, repos, maxPages*100)
	assert.Len(t, f.taken(), maxPages)
}

// An installation of an app made before workflows was asked for refuses it
// (422): the token comes without it, says so, and the installation isn't
// asked for it again for an hour, or until Forget.
func TestRepoTokenWithoutWorkflows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	var asked []map[string]string
	f := &fakeAPI{}
	f.handle = func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/widget/installation":
			writeJSON(w, http.StatusOK, map[string]any{"id": 11})
		case r.URL.Path == "/app/installations/11/access_tokens":
			var body struct {
				Permissions map[string]string `json:"permissions"`
			}
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			asked = append(asked, body.Permissions)
			if body.Permissions["workflows"] != "" {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "The permissions requested are not granted to this installation."})
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"token": "ghs_base", "expires_at": now.Add(time.Hour)})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		}
	}
	c, app := newTestClient(t, f, &now)

	token, err := c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.Equal(t, "ghs_base", token.Value)
	assert.True(t, token.WithoutWorkflows)
	require.Len(t, asked, 2)
	assert.Equal(t, "write", asked[0]["workflows"], "asked with workflows first")
	assert.Equal(t, basePermissions, asked[1])

	// The kept token says it too; the next one goes straight to the base.
	token, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.True(t, token.WithoutWorkflows)
	now = now.Add(56 * time.Minute)
	asked = nil
	token, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	assert.True(t, token.WithoutWorkflows)
	assert.Equal(t, []map[string]string{basePermissions}, asked)

	// An hour after the refusal it is asked for again, once the kept token
	// is due: the owner may have granted it since.
	now = now.Add(56 * time.Minute)
	asked = nil
	_, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	require.NotEmpty(t, asked)
	assert.Equal(t, "write", asked[0]["workflows"])

	// And at once after Forget (the app changed).
	c.Forget()
	asked = nil
	_, err = c.RepoToken(ctx, app, "acme/widget")
	require.NoError(t, err)
	require.NotEmpty(t, asked)
	assert.Equal(t, "write", asked[0]["workflows"])
}
