package native

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"sync"

	st "github.com/coder/agentapi/lib/screentracker"
)

// awaitLogin records that the agent of generation gen needs a login and
// starts it again after LoginRetryInterval, or sooner on RetryLogin.
func (c *Conversation) awaitLogin(ctx context.Context, gen int, resume Resume, login LoginRequired) {
	login = withLocalAccount(login)
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation {
		return
	}
	// The adapter that found no login is closed; the next start gets a
	// generation of its own, so the closed one can't change the state.
	c.generation++
	c.adapter = nil
	c.loginWaiting = true
	if c.setLoginLocked(login) {
		c.cfg.Logger.Warn("The agent needs a login", "agent", login.Agent, "command", login.Command)
		c.cfg.Emitter.EmitError(loginErrorText(login), st.ErrorLevelWarning)
	}
	wake := make(chan struct{})
	c.loginWake = wake
	go c.relaunchAfterWait(ctx, wake, c.generation, resume)
}

// relaunchAfterWait starts the agent again as generation gen once it waited
// for a login (closing wake cuts the wait short), unless the agent was
// replaced or closed meanwhile.
func (c *Conversation) relaunchAfterWait(ctx context.Context, wake <-chan struct{}, gen int, resume Resume) {
	if !c.waitForLogin(ctx, wake) {
		return
	}
	c.mu.Lock()
	current := gen == c.generation
	if current {
		c.loginWake = nil
	}
	c.mu.Unlock()
	if !current {
		return
	}
	if err := c.launch(ctx, gen, resume); err != nil {
		c.failStart(gen, err)
	}
}

// waitForLogin waits LoginRetryInterval or until wake is closed. It reports
// false if ctx ended first.
func (c *Conversation) waitForLogin(ctx context.Context, wake <-chan struct{}) bool {
	timer := c.cfg.Clock.NewTimer(c.cfg.LoginRetryInterval, "Conversation", "loginRetry")
	defer timer.Stop("Conversation", "loginRetry")
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	case <-wake:
	}
	return true
}

// setLoginLocked records that the agent waits for login and reports whether
// that is news. Observers only hear about changes.
func (c *Conversation) setLoginLocked(login LoginRequired) bool {
	if c.login != nil && *c.login == login {
		return false
	}
	c.login = &login
	c.cfg.Observer.LoginRequired(&login)
	return true
}

// localAccount returns the machine AgentAPI runs on and the user it runs
// as, which is where the user has to log the agent in.
var localAccount = sync.OnceValues(func() (host, username string) {
	host, _ = os.Hostname()
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	return host, username
})

// withLocalAccount fills in where to log in, unless the adapter said.
func withLocalAccount(login LoginRequired) LoginRequired {
	host, username := localAccount()
	if login.Host == "" {
		login.Host = host
	}
	if login.User == "" {
		login.User = username
	}
	return login
}

func loginErrorText(login LoginRequired) string {
	where := ""
	if login.Host != "" && login.User != "" {
		where = fmt.Sprintf(" on %s as %s", login.Host, login.User)
	}
	return fmt.Sprintf("%s Run `%s` in a terminal%s; AgentAPI picks up the login by itself.", login.Message, login.Command, where)
}

// LoginRequired returns what the agent waits for to log in, or nil.
func (c *Conversation) LoginRequired() *LoginRequired {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.login == nil {
		return nil
	}
	login := *c.login
	return &login
}

// RetryLogin checks again whether the user logged in. While the agent waits
// at startup it is started again now; a start that is under way already is
// that check. If the agent lost its login while running, it is replaced by
// a new process that resumes the session.
func (c *Conversation) RetryLogin(context.Context) error {
	c.mu.Lock()
	if c.login == nil {
		c.mu.Unlock()
		return ErrNoLoginRequired
	}
	if !c.started {
		if c.loginWake != nil {
			close(c.loginWake)
			c.loginWake = nil
		}
		c.mu.Unlock()
		return nil
	}
	old := c.retireAdapterLocked()
	gen, resume, runCtx := c.generation, c.resumeLocked(), c.ctx
	c.cfg.Emitter.EmitStatus(st.ConversationStatusInitializing)
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	c.launchAsync(runCtx, gen, resume)
	return nil
}
