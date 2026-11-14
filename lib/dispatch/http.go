package dispatch

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/github"
	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/jsonapi"
)

// MountDispatch adds POST /api/dispatch, for a signed-in browser or a
// client with the dispatch token.
func (s *Service) MountDispatch(r chi.Router, signedIn func(*http.Request) bool) {
	r.Post("/api/dispatch", func(w http.ResponseWriter, r *http.Request) {
		// A client with the dispatch token is the workflow's runner, even
		// on a hub without sign-in.
		token := s.dispatchToken(r)
		browser := !token && signedIn(r)
		if !browser && !token {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusUnauthorized, "unauthorized", "sign in to the hub, or send the dispatch token"))
			return
		}
		var req Request
		if !jsonapi.Read(w, r, &req) {
			return
		}
		ctx := r.Context()
		if browser {
			ctx = FromHub(ctx)
		}
		resp, err := s.Dispatch(ctx, req)
		if err != nil {
			jsonapi.WriteError(w, err)
			return
		}
		status := http.StatusOK
		if resp.Created {
			status = http.StatusCreated
		}
		jsonapi.Write(w, status, resp)
	})
}

func (s *Service) dispatchToken(r *http.Request) bool {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.config.DispatchToken != "" && hmac.Equal([]byte(bearer), []byte(s.config.DispatchToken))
}

// MountLauncher adds the route a launcher gets a job's GitHub token from,
// signed with the hub's secret.
func (s *Service) MountLauncher(r chi.Router) {
	r.Post(host.HubTokenPath, s.handleToken)
}

// handleToken answers a launcher's request for a job's GitHub token: a
// token for the job's repository alone, to a job that works now on the
// machine that asks and gets the hub's credentials.
func (s *Service) handleToken(w http.ResponseWriter, r *http.Request) {
	hostname, key, err := s.authLauncher(r)
	if err != nil {
		jsonapi.WriteError(w, err)
		return
	}
	job := s.job(key.String())
	if job == nil || job.Host != hostname || !job.working() || !job.Credentials {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusForbidden, "no_job", "%s has no job %s that gets the hub's token", hostname, key))
		return
	}
	app := s.currentApp()
	if app == nil {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusServiceUnavailable, "github_not_connected", "the hub has no GitHub App"))
		return
	}
	token, err := s.gh.RepoToken(r.Context(), app, key.Repo())
	if err != nil {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadGateway, "github_failed", "%v", err))
		return
	}
	if token.WithoutWorkflows {
		if _, said := s.noWorkflows.LoadOrStore(key.Repo(), true); !said {
			s.logger.Warn("The hub's GitHub App hasn't been granted the workflows permission, so a job's push that changes .github/workflows/ is refused: add Workflows (read and write) to the app's repository permissions, then accept the new permissions on its installation",
				"repo", key.Repo(), "settings", app.SettingsURL(s.gh.Web))
		}
	}
	jsonapi.Write(w, http.StatusOK, host.Token{Value: token.Value, ExpiresAt: token.ExpiresAt})
}

// authLauncher reads a launcher's token request: signed with the hub's
// secret, not answered before, and with the launcher's token of the
// machine it names. It returns the machine and the job asked about, or a
// *jsonapi.Error.
func (s *Service) authLauncher(r *http.Request) (string, host.Key, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, jsonapi.MaxBody))
	if err != nil {
		return "", host.Key{}, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "unreadable request")
	}
	now := s.config.Now()
	if err := instanceapi.Verify(s.config.Secret, r.Header, body, now); err != nil {
		return "", host.Key{}, jsonapi.Errorf(http.StatusUnauthorized, "unauthorized", "invalid signature: %v", err)
	}
	if s.replayedToken(r.Header.Get(instanceapi.HeaderSignature), now) {
		return "", host.Key{}, jsonapi.Errorf(http.StatusConflict, "replayed", "the request was answered already")
	}
	var req host.HubTokenRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", host.Key{}, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "%v", err)
	}
	key, err := host.ParseKey(req.Job)
	if err != nil {
		return "", host.Key{}, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "%v", err)
	}
	// The machine proves its name with its launcher's token, which the
	// hub has from its events: the secret alone, which every machine that
	// joined has, doesn't name it.
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	machine := s.hosts[req.Hostname]
	known := machine != nil && machine.Token != "" && hmac.Equal([]byte(bearer), []byte(machine.Token))
	s.mu.Unlock()
	if !known {
		return "", host.Key{}, jsonapi.Errorf(http.StatusUnauthorized, "unauthorized", "send the launcher's token of %s", req.Hostname)
	}
	return req.Hostname, key, nil
}

// replayedToken reports whether a token request with signature was taken
// before, and takes it. The signature covers the body and its time, not
// the delivery header: a copy is told by its signature, which holds for
// 2*instanceapi.MaxSkew at most.
func (s *Service) replayedToken(signature string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, at := range s.tokenRequests {
		if now.Sub(at) > 2*instanceapi.MaxSkew {
			delete(s.tokenRequests, id)
		}
	}
	_, seen := s.tokenRequests[signature]
	s.tokenRequests[signature] = now
	return seen
}

// GitHubStatus is GET /api/github.
type GitHubStatus struct {
	Connected bool   `json:"connected"`
	Slug      string `json:"slug,omitempty"`
	Owner     string `json:"owner,omitempty"`
	// OwnerType is the owner's kind, "User" or "Organization"; empty
	// while the hub doesn't know it.
	OwnerType string `json:"owner_type,omitempty"`
	// HTMLURL is the app's public page; SettingsURL its settings, under
	// its owner's (empty while the owner's kind isn't known); InstallURL
	// installs it on an account, or picks the repositories of one it is
	// installed on.
	HTMLURL       string               `json:"html_url,omitempty"`
	SettingsURL   string               `json:"settings_url,omitempty"`
	InstallURL    string               `json:"install_url,omitempty"`
	Installations []InstallationStatus `json:"installations"`
}

// InstallationStatus is an installation of the hub's app, with its links.
type InstallationStatus struct {
	ID      int64  `json:"id"`
	Account string `json:"account"`
	// AccountType is "User" or "Organization"; empty while the hub doesn't
	// know it.
	AccountType string `json:"account_type,omitempty"`
	// RepositorySelection is "all" or "selected".
	RepositorySelection string `json:"repository_selection"`
	// AvatarURL is the account's picture on GitHub; SettingsURL the
	// installation's page, where the account picks its repositories (empty
	// while the account's kind isn't known).
	AvatarURL   string `json:"avatar_url"`
	SettingsURL string `json:"settings_url,omitempty"`
}

// githubStatus is what GET /api/github says of app (nil: not connected).
func githubStatus(web string, app *github.App) GitHubStatus {
	status := GitHubStatus{Installations: []InstallationStatus{}}
	if app == nil {
		return status
	}
	web = strings.TrimSuffix(web, "/")
	status.Connected, status.Slug, status.Owner = true, app.Slug, app.Owner
	// An app registered before the hub kept its owner's kind: its owner's
	// own installation says it.
	known := *app
	for _, inst := range app.Installations {
		if known.OwnerType == "" && inst.AccountType != "" && strings.EqualFold(inst.Account, app.Owner) {
			known.OwnerType = inst.AccountType
		}
	}
	status.OwnerType = known.OwnerType
	status.HTMLURL = web + "/apps/" + url.PathEscape(app.Slug)
	status.SettingsURL = known.SettingsURL(web)
	status.InstallURL = app.InstallURL(web, "")
	for _, inst := range app.Installations {
		status.Installations = append(status.Installations, InstallationStatus{
			ID: inst.ID, Account: inst.Account, AccountType: inst.AccountType, RepositorySelection: inst.RepositorySelection,
			AvatarURL: web + "/" + url.PathEscape(inst.Account) + ".png?size=96", SettingsURL: inst.SettingsURL(web),
		})
	}
	return status
}

// MountSession adds the routes for a signed-in browser: the machines, the
// jobs, the GitHub App's setup and the issues.
func (s *Service) MountSession(r chi.Router) {
	r.Get("/api/hosts", func(w http.ResponseWriter, _ *http.Request) {
		jsonapi.Write(w, http.StatusOK, HostsBody{Hosts: s.Hosts()})
	})
	r.Delete("/api/hosts/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ForgetHost(chi.URLParam(r, "name")); err != nil {
			jsonapi.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/api/jobs", func(w http.ResponseWriter, _ *http.Request) {
		jsonapi.Write(w, http.StatusOK, JobsBody{Jobs: s.Jobs()})
	})
	r.Get("/api/github", func(w http.ResponseWriter, _ *http.Request) {
		jsonapi.Write(w, http.StatusOK, githubStatus(s.gh.Web, s.currentApp()))
	})
	r.Delete("/api/github", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.app = nil
		s.mu.Unlock()
		s.gh.Forget()
		if s.config.AppPath != "" {
			if err := os.Remove(s.config.AppPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				jsonapi.WriteError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/api/github/repos", s.listRepos)
	r.Get("/api/github/issues", s.listIssues)
	r.Post("/api/github/issues", s.createIssue)
	r.Get("/github/connect", s.connect)
	r.Get("/github/callback", s.callback)
	r.Get("/github/setup", s.setup)
}

func (s *Service) listRepos(w http.ResponseWriter, r *http.Request) {
	app := s.currentApp()
	if app == nil {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusPreconditionFailed, "github_not_connected", "connect the hub to GitHub first"))
		return
	}
	repos := []github.Repo{}
	for _, inst := range app.Installations {
		token, err := s.gh.InstallationToken(r.Context(), app, inst.ID)
		if err != nil {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadGateway, "github_failed", "%v", err))
			return
		}
		list, err := s.gh.With(token).Repos(r.Context())
		if err != nil {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadGateway, "github_failed", "%v", err))
			return
		}
		repos = append(repos, list...)
	}
	sortRepos(repos)
	jsonapi.Write(w, http.StatusOK, ReposBody{Repos: repos})
}

func (s *Service) listIssues(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if _, _, err := host.ParseRepo(repo); err != nil {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "%v", err))
		return
	}
	as, ok, err := s.repoClient(r.Context(), s.currentApp(), repo)
	if err == nil && !ok {
		err = jsonapi.Errorf(http.StatusPreconditionFailed, "github_not_connected", "connect the hub to GitHub first")
	}
	if err != nil {
		jsonapi.WriteError(w, err)
		return
	}
	issues, err := as.Issues(r.Context(), repo)
	if err != nil {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadGateway, "github_failed", "%v", err))
		return
	}
	jsonapi.Write(w, http.StatusOK, IssuesBody{Issues: issues})
}

// sortRepos puts the repositories pushed to or changed latest first: the
// page reads the issues of the first ones.
func sortRepos(repos []github.Repo) {
	slices.SortStableFunc(repos, func(a, b github.Repo) int { return b.Latest().Compare(a.Latest()) })
}

// CreateIssueRequest is POST /api/github/issues.
type CreateIssueRequest struct {
	Repo   string   `json:"repo"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
	// Assign starts work on the new issue.
	Assign *Assign `json:"assign,omitempty"`
}

// Assign is who works on a new issue: an agent (empty is the machine's
// default) and a machine (empty lets the hub pick).
type Assign struct {
	Agent string `json:"agent,omitempty"`
	Host  string `json:"host,omitempty"`
}

// HostsBody is GET /api/hosts, JobsBody GET /api/jobs, ReposBody GET
// /api/github/repos and IssuesBody GET /api/github/issues.
type (
	HostsBody struct {
		Hosts []Host `json:"hosts"`
	}
	JobsBody struct {
		Jobs []Job `json:"jobs"`
	}
	ReposBody struct {
		Repos []github.Repo `json:"repos"`
	}
	IssuesBody struct {
		Issues []github.Issue `json:"issues"`
	}
)

// CreateIssueResponse is its answer.
type CreateIssueResponse struct {
	Issue github.Issue `json:"issue"`
	Job   *Job         `json:"job,omitempty"`
}

func (s *Service) createIssue(w http.ResponseWriter, r *http.Request) {
	var req CreateIssueRequest
	if !jsonapi.Read(w, r, &req) {
		return
	}
	if _, _, err := host.ParseRepo(req.Repo); err != nil || strings.TrimSpace(req.Title) == "" {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "an issue has a repo (owner/name) and a title"))
		return
	}
	as, ok, err := s.repoClient(r.Context(), s.currentApp(), req.Repo)
	if err == nil && !ok {
		err = jsonapi.Errorf(http.StatusPreconditionFailed, "github_not_connected", "connect the hub to GitHub first")
	}
	if err != nil {
		jsonapi.WriteError(w, err)
		return
	}
	labels := req.Labels
	if req.Assign != nil {
		labels = append(labels, agentLabel(req.Assign.Agent))
	}
	body := strings.TrimRight(req.Body, "\n") + "\n\n_由 agentapi hub 建立。_"
	issue, err := as.CreateIssue(r.Context(), req.Repo, req.Title, body, labels)
	if err != nil {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadGateway, "github_failed", "%v", err))
		return
	}
	resp := CreateIssueResponse{Issue: issue}
	if req.Assign != nil {
		dispatched, err := s.Dispatch(r.Context(), Request{
			Action: ActionStart, DeliveryID: fmt.Sprintf("hub-issue-%s#%d", req.Repo, issue.Number),
			Repo: req.Repo, Issue: issue.Number, Agent: req.Assign.Agent, Host: req.Assign.Host,
		})
		if err != nil {
			jsonapi.WriteError(w, err)
			return
		}
		resp.Job = &dispatched.Job
	}
	jsonapi.Write(w, http.StatusCreated, resp)
}

func agentLabel(agent string) string {
	if agent == "" {
		return "agent"
	}
	return "agent:" + agent
}

// newState is a one-time state for the GitHub setup, good for ten
// minutes.
func (s *Service) newState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)
	s.mu.Lock()
	now := s.config.Now()
	for k, at := range s.states {
		if now.After(at) {
			delete(s.states, k)
		}
	}
	s.states[state] = now.Add(10 * time.Minute)
	s.mu.Unlock()
	return state
}

func (s *Service) takeState(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.states[state]
	delete(s.states, state)
	return ok && s.config.Now().Before(at)
}

// origin is the hub's address as the browser reached it: where GitHub
// sends it back to, which may be an address only this network reaches.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// Manifest is the GitHub App the hub registers: private, without a
// webhook (the runner brings the events), with the permissions its jobs
// need.
func Manifest(hubURL, name string) map[string]any {
	return map[string]any{
		"name":            name,
		"url":             hubURL,
		"redirect_url":    hubURL + "/github/callback",
		"setup_url":       hubURL + "/github/setup",
		"setup_on_update": true,
		"public":          false,
		"hook_attributes": map[string]any{"url": hubURL + "/github/unused", "active": false},
		"default_permissions": map[string]string{
			"contents": "write", "pull_requests": "write", "issues": "write", "workflows": "write", "metadata": "read",
		},
	}
}

var connectPage = template.Must(template.New("").Parse(`<!doctype html>
<html lang="zh-Hant"><head><meta charset="utf-8"><title>連接 GitHub</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:3rem auto">
<h1>建立 agentapi hub 的 GitHub App</h1>
<p>按下按鈕後到 GitHub 確認建立。名稱可以在 GitHub 上修改（GitHub 上不能重名）。建立後會接著請你選擇要安裝的 repository。</p>
{{if .Current}}<p>這個 hub 現在用的是 {{.Current}}。新的 App 建立後才會取代它；在 GitHub 上取消的話，{{.Current}} 照常運作。舊的 App 會留在 GitHub 上，不用時到它的設定頁刪除。</p>{{end}}
<form id="f" method="post" action="{{.Action}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<button type="submit">在 GitHub 建立 App{{if .Org}}（組織 {{.Org}}）{{end}}</button>
</form>
<p><small>要建在組織底下時，在網址加上 <code>?org=&lt;組織&gt;</code>。</small></p>
</body></html>`))

var donePage = template.Must(template.New("").Parse(`<!doctype html>
<html lang="zh-Hant"><head><meta charset="utf-8"><title>GitHub</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:3rem auto">
<h1>{{.Title}}</h1><p>{{.Message}}</p>
{{if .Link}}<p><a href="{{.Link}}">{{.LinkText}}</a></p>{{end}}
<p><a href="/">回到 hub</a></p>
</body></html>`))

type donePageData struct{ Title, Message, Link, LinkText string }

func (s *Service) connect(w http.ResponseWriter, r *http.Request) {
	org := r.URL.Query().Get("org")
	action := strings.TrimSuffix(s.gh.Web, "/") + "/settings/apps/new"
	if org != "" {
		if _, _, err := host.ParseRepo(org + "/x"); err != nil {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "org %q isn't an organization's name", org))
			return
		}
		action = strings.TrimSuffix(s.gh.Web, "/") + "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	manifest, _ := json.Marshal(Manifest(origin(r), "agentapi-hub-"+hex.EncodeToString(suffix)))
	action += "?state=" + url.QueryEscape(s.newState())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	current := ""
	if app := s.currentApp(); app != nil {
		current = app.Slug
	}
	_ = connectPage.Execute(w, map[string]string{"Action": action, "Manifest": string(manifest), "Org": org, "Current": current})
}

func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !s.takeState(q.Get("state")) {
		s.page(w, http.StatusBadRequest, donePageData{Title: "連接失敗", Message: "這個連結已過期或不是這個 hub 發出的，請重新開始。", Link: "/github/connect", LinkText: "重新連接 GitHub"})
		return
	}
	app, err := s.gh.ConvertManifest(r.Context(), q.Get("code"))
	if err != nil {
		s.page(w, http.StatusBadGateway, donePageData{Title: "連接失敗", Message: err.Error(), Link: "/github/connect", LinkText: "重新連接 GitHub"})
		return
	}
	app.HubURL = origin(r)
	// The new app replaces the one the hub had, if any, only now: a
	// connect left on GitHub keeps the hub's app working.
	if err := s.saveApp(&app); err != nil {
		s.page(w, http.StatusInternalServerError, donePageData{Title: "連接失敗", Message: err.Error()})
		return
	}
	// The tokens kept are the old app's.
	s.gh.Forget()
	s.logger.Info("Registered the hub's GitHub App", "slug", app.Slug, "owner", app.Owner)
	http.Redirect(w, r, app.InstallURL(s.gh.Web, s.newState()), http.StatusFound)
}

func (s *Service) setup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// GitHub brings back the state of the install link the hub gave; an
	// installation made on GitHub itself comes without one. Either way
	// GitHub's answer below says whether it is this app's.
	if state := q.Get("state"); state != "" && !s.takeState(state) {
		s.page(w, http.StatusBadRequest, donePageData{Title: "安裝失敗", Message: "這個連結已過期或不是這個 hub 發出的。"})
		return
	}
	app := s.currentApp()
	if app == nil {
		s.page(w, http.StatusPreconditionFailed, donePageData{Title: "尚未連接 GitHub", Message: "先建立 hub 的 GitHub App。", Link: "/github/connect", LinkText: "連接 GitHub"})
		return
	}
	id, err := strconv.ParseInt(q.Get("installation_id"), 10, 64)
	if err != nil {
		s.page(w, http.StatusBadRequest, donePageData{Title: "安裝失敗", Message: "GitHub 沒有帶回 installation_id。"})
		return
	}
	// The installation ID comes from the browser: GitHub's answer says
	// whether it is this app's.
	inst, err := s.gh.AppInstallation(r.Context(), app, id)
	if err != nil {
		s.page(w, http.StatusBadGateway, donePageData{Title: "安裝失敗", Message: err.Error()})
		return
	}
	s.mu.Lock()
	updated := *app
	updated.Installations = nil
	for _, i := range app.Installations {
		if i.ID != inst.ID {
			updated.Installations = append(updated.Installations, i)
		}
	}
	updated.Installations = append(updated.Installations, inst)
	s.mu.Unlock()
	if err := s.saveApp(&updated); err != nil {
		s.page(w, http.StatusInternalServerError, donePageData{Title: "安裝失敗", Message: err.Error()})
		return
	}
	s.gh.Forget()
	s.logger.Info("The hub's GitHub App is installed", "account", inst.Account, "installation", inst.ID)
	s.page(w, http.StatusOK, donePageData{
		Title:   "GitHub 已連接",
		Message: fmt.Sprintf("%s 已安裝在 %s。之後在 issue 加上 agent 或 agent:<agent> label 就會派工。", app.Slug, inst.Account),
		Link:    app.InstallURL(s.gh.Web, ""), LinkText: "調整安裝的 repository",
	})
}

func (s *Service) saveApp(app *github.App) error {
	if s.config.AppPath != "" {
		data, err := json.MarshalIndent(app, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(s.config.AppPath), 0o700); err != nil {
			return err
		}
		if err := atomicfile.Write(s.config.AppPath, ".github-app-*", data); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
	return nil
}

func (s *Service) page(w http.ResponseWriter, status int, data donePageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = donePage.Execute(w, data)
}
