// Package github is the hub's GitHub App: registering it from a manifest,
// the app's JWT and the installation tokens it gives out for one
// repository at a time, and the REST calls the issue dispatch makes as the
// app (issues, comments, labels, branches). It has no dependencies beyond
// the standard library.
package github

import (
	"bytes"
	"cmp"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// App is a GitHub App the hub registered, with its secrets.
type App struct {
	ID            int64  `json:"app_id"`
	Slug          string `json:"slug"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	// BotUserID is the user ID of <slug>[bot], for its commits' email.
	BotUserID int64 `json:"bot_user_id"`
	// Owner is the account the app belongs to; OwnerType is its kind,
	// "User" or "Organization", where its settings live on GitHub (empty
	// for an app registered before the hub kept it).
	Owner     string `json:"owner"`
	OwnerType string `json:"owner_type,omitempty"`
	// HubURL is the hub's address the app was registered from, for links.
	HubURL        string         `json:"hub_url"`
	Installations []Installation `json:"installations"`
}

// Installation is the app installed on an account.
type Installation struct {
	ID      int64  `json:"id"`
	Account string `json:"account"`
	// AccountType is the account's kind, "User" or "Organization" (empty
	// for one installed before the hub kept it).
	AccountType         string `json:"account_type,omitempty"`
	RepositorySelection string `json:"repository_selection"`
}

// SettingsURL is the app's settings page on GitHub, under its owner's
// settings; "" while the owner's kind isn't known.
func (a *App) SettingsURL(web string) string {
	base := strings.TrimSuffix(web, "/")
	switch a.OwnerType {
	case "User":
		return base + "/settings/apps/" + url.PathEscape(a.Slug)
	case "Organization":
		return base + "/organizations/" + url.PathEscape(a.Owner) + "/settings/apps/" + url.PathEscape(a.Slug)
	}
	return ""
}

// SettingsURL is the installation's page on GitHub, where its account
// picks the repositories it is on; "" while the account's kind isn't
// known.
func (i Installation) SettingsURL(web string) string {
	base := strings.TrimSuffix(web, "/")
	id := strconv.FormatInt(i.ID, 10)
	switch i.AccountType {
	case "User":
		return base + "/settings/installations/" + id
	case "Organization":
		return base + "/organizations/" + url.PathEscape(i.Account) + "/settings/installations/" + id
	}
	return ""
}

// BotName and BotEmail are who the app's commits are by.
func (a *App) BotName() string { return a.Slug + "[bot]" }

func (a *App) BotEmail() string {
	return fmt.Sprintf("%d+%s[bot]@users.noreply.github.com", a.BotUserID, a.Slug)
}

// InstallURL is where a user installs the app on their repositories.
func (a *App) InstallURL(web, state string) string {
	u := strings.TrimSuffix(web, "/") + "/apps/" + url.PathEscape(a.Slug) + "/installations/new"
	if state != "" {
		u += "?state=" + url.QueryEscape(state)
	}
	return u
}

// Token is an installation token.
type Token struct {
	Value     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Permissions are what a token for a job, and the hub's own, may do.
var Permissions = map[string]string{
	"contents":      "write",
	"pull_requests": "write",
	"issues":        "write",
}

// Client calls GitHub.
type Client struct {
	// API is the REST API's root, by default https://api.github.com; Web
	// is github.com's, by default https://github.com.
	API  string
	Web  string
	HTTP *http.Client
	Now  func() time.Time

	mu sync.Mutex
	// tokens are the installation tokens the client got, by repository,
	// used until five minutes before they expire.
	tokens map[string]Token
	// installations are the installation of each repository.
	installations map[string]int64
	// generation counts Forget: a token asked for before one isn't kept.
	generation int
}

// NewClient returns a client of github.com.
func NewClient() *Client {
	return &Client{API: "https://api.github.com", Web: "https://github.com", HTTP: &http.Client{Timeout: 30 * time.Second}, Now: time.Now}
}

// Error is GitHub's answer to a call that failed.
type Error struct {
	Status  int
	Message string
	Call    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("GitHub %s: %d %s", e.Call, e.Status, e.Message)
}

// IsStatus reports whether err is GitHub's answer status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// do calls the API with auth ("Bearer <jwt>" or "token <token>") and reads
// the answer into out.
func (c *Client) do(ctx context.Context, method, path, auth string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.API, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return &Error{Status: resp.StatusCode, Message: cmp.Or(e.Message, http.StatusText(resp.StatusCode)), Call: method + " " + path}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ConvertManifest exchanges the code GitHub gave the hub for the app it
// registered from the manifest, with its private key.
func (c *Client) ConvertManifest(ctx context.Context, code string) (App, error) {
	var out struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
		Owner         struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"owner"`
	}
	if err := c.do(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &out); err != nil {
		return App{}, err
	}
	app := App{ID: out.ID, Slug: out.Slug, ClientID: out.ClientID, ClientSecret: out.ClientSecret, PEM: out.PEM, WebhookSecret: out.WebhookSecret, Owner: out.Owner.Login, OwnerType: out.Owner.Type}
	if _, err := parseKey(app.PEM); err != nil {
		return App{}, err
	}
	if id, err := c.UserID(ctx, app.BotName()); err == nil {
		app.BotUserID = id
	}
	return app, nil
}

// UserID is the ID of the user login.
func (c *Client) UserID(ctx context.Context, login string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := c.do(ctx, http.MethodGet, "/users/"+url.PathEscape(login), "", nil, &out)
	return out.ID, err
}

func parseKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("the app's private key isn't PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("read the app's private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the app's private key isn't RSA")
	}
	return key, nil
}

// JWT is the app's token for its own calls: RS256, valid ten minutes at
// most (issued a minute back, for clock drift).
func (c *Client) JWT(app *App) (string, error) {
	key, err := parseKey(app.PEM)
	if err != nil {
		return "", err
	}
	now := c.Now()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(app.ID, 10),
	})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// AppInstallation reads the app's installation id, to check that a user
// installed this app.
func (c *Client) AppInstallation(ctx context.Context, app *App, id int64) (Installation, error) {
	jwt, err := c.JWT(app)
	if err != nil {
		return Installation{}, err
	}
	var out struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		RepositorySelection string `json:"repository_selection"`
	}
	if err := c.do(ctx, http.MethodGet, "/app/installations/"+strconv.FormatInt(id, 10), "Bearer "+jwt, nil, &out); err != nil {
		return Installation{}, err
	}
	return Installation{ID: out.ID, Account: out.Account.Login, AccountType: out.Account.Type, RepositorySelection: out.RepositorySelection}, nil
}

// RepoToken is an installation token for repo (owner/name) alone, with
// Permissions: kept until five minutes before it expires. A repository the
// app isn't installed on answers 404.
func (c *Client) RepoToken(ctx context.Context, app *App, repo string) (Token, error) {
	c.mu.Lock()
	if t, ok := c.tokens[repo]; ok && c.Now().Before(t.ExpiresAt.Add(-5*time.Minute)) {
		c.mu.Unlock()
		return t, nil
	}
	installation, known := c.installations[repo]
	generation := c.generation
	c.mu.Unlock()
	jwt, err := c.JWT(app)
	if err != nil {
		return Token{}, err
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return Token{}, fmt.Errorf("repository %q: want owner/name", repo)
	}
	if !known {
		var out struct {
			ID int64 `json:"id"`
		}
		if err := c.do(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/installation", "Bearer "+jwt, nil, &out); err != nil {
			return Token{}, err
		}
		installation = out.ID
	}
	var t Token
	body := map[string]any{"repositories": []string{name}, "permissions": Permissions}
	if err := c.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(installation, 10)+"/access_tokens", "Bearer "+jwt, body, &t); err != nil {
		if IsStatus(err, http.StatusNotFound) {
			c.mu.Lock()
			delete(c.installations, repo)
			c.mu.Unlock()
		}
		return Token{}, err
	}
	c.mu.Lock()
	if c.generation == generation {
		if c.tokens == nil {
			c.tokens, c.installations = map[string]Token{}, map[string]int64{}
		}
		c.tokens[repo], c.installations[repo] = t, installation
	}
	c.mu.Unlock()
	return t, nil
}

// Forget drops the tokens kept, as when the app changes; a token asked for
// meanwhile isn't kept either.
func (c *Client) Forget() {
	c.mu.Lock()
	c.tokens, c.installations = nil, nil
	c.generation++
	c.mu.Unlock()
}

// Issue is an issue or a pull request, as the dispatch reads it.
type Issue struct {
	Number  int     `json:"number"`
	Title   string  `json:"title"`
	Body    string  `json:"body"`
	State   string  `json:"state"`
	HTMLURL string  `json:"html_url"`
	Labels  []Label `json:"labels"`
	User    User    `json:"user"`
	// PullRequest is set on a pull request, which the issues API lists too.
	PullRequest *PullRequestRef `json:"pull_request,omitempty"`
}

// User is an issue's author.
type User struct {
	Login string `json:"login"`
}

// PullRequestRef marks an issue that is a pull request.
type PullRequestRef struct {
	URL string `json:"url,omitempty"`
}

// Label is an issue's label.
type Label struct {
	Name string `json:"name"`
}

// Repo is a repository.
type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	HTMLURL       string `json:"html_url"`
	// PushedAt and UpdatedAt are when it was last pushed to, and changed
	// (an issue, a setting): how recent it is.
	PushedAt  time.Time `json:"pushed_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Latest is when the repository was last pushed to or changed.
func (r Repo) Latest() time.Time {
	if r.PushedAt.After(r.UpdatedAt) {
		return r.PushedAt
	}
	return r.UpdatedAt
}

// As calls the API with an installation token.
type As struct {
	c     *Client
	token string
}

// With is a client of the calls the app makes with t.
func (c *Client) With(t Token) As { return As{c: c, token: t.Value} }

func (a As) do(ctx context.Context, method, path string, body, out any) error {
	return a.c.do(ctx, method, path, "token "+a.token, body, out)
}

func repoPath(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func (a As) Repo(ctx context.Context, repo string) (Repo, error) {
	var out Repo
	err := a.do(ctx, http.MethodGet, repoPath(repo), nil, &out)
	return out, err
}

func (a As) Issue(ctx context.Context, repo string, number int) (Issue, error) {
	var out Issue
	err := a.do(ctx, http.MethodGet, repoPath(repo)+"/issues/"+strconv.Itoa(number), nil, &out)
	return out, err
}

// maxPages bounds the pages of a list the client reads (100 items each).
const maxPages = 10

// Issues are repo's open issues, newest first, without pull requests: up
// to maxPages pages.
func (a As) Issues(ctx context.Context, repo string) ([]Issue, error) {
	var issues []Issue
	for page := 1; page <= maxPages; page++ {
		var out []Issue
		if err := a.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues?state=open&per_page=100&page=%d", repoPath(repo), page), nil, &out); err != nil {
			return nil, err
		}
		for _, issue := range out {
			if issue.PullRequest == nil {
				issues = append(issues, issue)
			}
		}
		if len(out) < 100 {
			break
		}
	}
	return issues, nil
}

func (a As) CreateIssue(ctx context.Context, repo, title, body string, labels []string) (Issue, error) {
	var out Issue
	in := map[string]any{"title": title, "body": body}
	if len(labels) > 0 {
		in["labels"] = labels
	}
	err := a.do(ctx, http.MethodPost, repoPath(repo)+"/issues", in, &out)
	return out, err
}

// Comment adds a comment to issue (or pull request) number, returning its
// ID.
func (a As) Comment(ctx context.Context, repo string, number int, body string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	err := a.do(ctx, http.MethodPost, repoPath(repo)+"/issues/"+strconv.Itoa(number)+"/comments", map[string]string{"body": body}, &out)
	return out.ID, err
}

func (a As) EditComment(ctx context.Context, repo string, id int64, body string) error {
	return a.do(ctx, http.MethodPatch, repoPath(repo)+"/issues/comments/"+strconv.FormatInt(id, 10), map[string]string{"body": body}, nil)
}

// RemoveLabel removes a label; one the issue doesn't have is no error.
func (a As) RemoveLabel(ctx context.Context, repo string, number int, label string) error {
	err := a.do(ctx, http.MethodDelete, repoPath(repo)+"/issues/"+strconv.Itoa(number)+"/labels/"+url.PathEscape(label), nil, nil)
	if IsStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// DeleteBranch deletes a branch; one already gone is no error.
func (a As) DeleteBranch(ctx context.Context, repo, branch string) error {
	err := a.do(ctx, http.MethodDelete, repoPath(repo)+"/git/refs/heads/"+escapeRef(branch), nil, nil)
	if IsStatus(err, http.StatusNotFound) || IsStatus(err, http.StatusUnprocessableEntity) {
		return nil
	}
	return err
}

// Repos are the repositories the installation of t reaches.
func (a As) Repos(ctx context.Context) ([]Repo, error) {
	var repos []Repo
	for page := 1; page <= maxPages; page++ {
		var out struct {
			Repositories []Repo `json:"repositories"`
		}
		if err := a.do(ctx, http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), nil, &out); err != nil {
			return nil, err
		}
		repos = append(repos, out.Repositories...)
		if len(out.Repositories) < 100 {
			break
		}
	}
	return repos, nil
}

// InstallationToken is a token of installation id for all its
// repositories, to list them.
func (c *Client) InstallationToken(ctx context.Context, app *App, id int64) (Token, error) {
	jwt, err := c.JWT(app)
	if err != nil {
		return Token{}, err
	}
	var t Token
	err = c.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens", "Bearer "+jwt, map[string]any{"permissions": map[string]string{"metadata": "read"}}, &t)
	return t, err
}

func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
