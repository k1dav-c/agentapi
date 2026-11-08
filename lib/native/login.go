package native

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"sync"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
)

// awaitLogin records that the agent of generation gen needs a login and
// starts it again after LoginRetryInterval, or sooner on RetryLogin.
func (c *Conversation) awaitLogin(ctx context.Context, gen int, resume Resume, login agentstate.LoginRequired) {
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
	if c.setLoginLocked(login) {
		c.cfg.Logger.Warn("The agent needs a login", "agent", login.Agent, "command", login.Command)
		c.cfg.Emitter.EmitError(loginErrorText(login), conv.ErrorLevelWarning)
	}
	c.awaitLoginLocked(ctx, resume)
}

// awaitLoginLocked starts the agent again, on the session resume says,
// after LoginRetryInterval, or sooner on RetryLogin; a start that finds no
// login waits again.
func (c *Conversation) awaitLoginLocked(ctx context.Context, resume Resume) {
	c.loginWaiting = true
	c.relaunchLocked(ctx, resume, c.cfg.LoginRetryInterval, "loginRetry")
}

// setLoginLocked records that the agent waits for login and reports whether
// that is news. Observers only hear about changes.
func (c *Conversation) setLoginLocked(login agentstate.LoginRequired) bool {
	if c.login != nil && *c.login == login {
		return false
	}
	c.login = &login
	c.cfg.Observer.LoginRequired(&login)
	return true
}

// clearLoginLocked records that the agent no longer waits for a login,
// and tells the observer if it did.
func (c *Conversation) clearLoginLocked() {
	if c.login != nil {
		c.login = nil
		c.cfg.Observer.LoginRequired(nil)
	}
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
func withLocalAccount(login agentstate.LoginRequired) agentstate.LoginRequired {
	host, username := localAccount()
	if login.Host == "" {
		login.Host = host
	}
	if login.User == "" {
		login.User = username
	}
	return login
}

func loginErrorText(login agentstate.LoginRequired) string {
	where := ""
	if login.Host != "" && login.User != "" {
		where = fmt.Sprintf(" on %s as %s", login.Host, login.User)
	}
	return fmt.Sprintf("%s Run `%s` in a terminal%s; AgentAPI picks up the login by itself.", login.Message, login.Command, where)
}

// LoginRequired returns what the agent waits for to log in, or nil.
func (c *Conversation) LoginRequired() *agentstate.LoginRequired {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.login == nil {
		return nil
	}
	login := *c.login
	return &login
}

// RetryLogin checks again whether the user logged in. While the agent waits
// to start again it is started now; a start that is under way already is
// that check. If the agent lost its login while running, it is replaced by
// a new process that resumes the session.
func (c *Conversation) RetryLogin(context.Context) error {
	c.mu.Lock()
	if c.login == nil {
		c.mu.Unlock()
		return ErrNoLoginRequired
	}
	if !c.started {
		c.wakeRelaunchLocked()
		c.mu.Unlock()
		return nil
	}
	old := c.retireAdapterLocked()
	c.restarts = 0
	gen, resume, runCtx := c.generation, c.resumeLocked(), c.ctx
	c.cfg.Emitter.EmitStatus(conv.StatusInitializing)
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	c.launchAsync(runCtx, gen, resume, true)
	return nil
}
