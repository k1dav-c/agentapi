package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/registry"
)

// servedNative is a native server that serve runs, with a fake agent.
type servedNative struct {
	t       *testing.T
	url     string
	factory *nativetest.Factory
	// served gets what serve returned.
	served <-chan error
}

// serveNative runs serve with a native server whose agents factory
// creates, on a free port, until ctx ends; self restarts it, if set.
func serveNative(ctx context.Context, t *testing.T, factory *nativetest.Factory, self *selfExec) *servedNative {
	t.Helper()
	return serveNativeWith(ctx, t, factory, self, nil)
}

// serveNativeWith is serveNative with configure adjusting the server's
// configuration.
func serveNativeWith(ctx context.Context, t *testing.T, factory *nativetest.Factory, self *selfExec, configure func(*httpapi.ServerConfig)) *servedNative {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registryDir := t.TempDir()
	config := httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		Native:         httpapi.NativeConfig{Factory: factory.New, RestartDelays: []time.Duration{time.Millisecond, time.Millisecond}},
		Bind:           "127.0.0.1",
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		RegistryDir:    registryDir,
	}
	if configure != nil {
		configure(&config)
	}
	srv, err := httpapi.NewServer(logctx.WithLogger(ctx, logger), config)
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- serve(ctx, logger, srv, &runningAgent{factory: factory.New}, self) }()
	// The registry entry has the port the server listens on.
	var url string
	require.Eventually(t, func() bool {
		entries, _ := registry.List(registryDir)
		if len(entries) == 1 {
			url = entries[0].URL
		}
		return url != ""
	}, 5*time.Second, 5*time.Millisecond)
	return &servedNative{t: t, url: url, factory: factory, served: served}
}

func (s *servedNative) do(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, s.url+path, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func (s *servedNative) lifecycle() httpapi.LifecycleState {
	resp, err := s.do(http.MethodGet, "/status")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var status httpapi.StatusChangeBody
	if json.NewDecoder(resp.Body).Decode(&status) != nil {
		return ""
	}
	return status.Lifecycle
}

func (s *servedNative) awaitLifecycle(want httpapi.LifecycleState) {
	s.t.Helper()
	require.Eventually(s.t, func() bool { return s.lifecycle() == want }, 5*time.Second, 5*time.Millisecond, "lifecycle %s", want)
}

// A native agent that keeps failing leaves the server serving, with the
// lifecycle failed, until a restart starts a new agent.
func TestServeOutlivesTheNativeAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sessions atomic.Int32
	s := serveNative(ctx, t, &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.Ref = fmt.Sprintf("session-%d", sessions.Add(1))
			return
		}
		a.StartErr = errors.New("the session can't be read")
	}}, nil)
	s.awaitLifecycle(httpapi.LifecycleReady)

	s.factory.Last().Exit(errors.New("exit status 1"))
	s.awaitLifecycle(httpapi.LifecycleFailed)
	assert.Len(t, s.factory.Adapters(), 3, "the first agent and two attempts")
	select {
	case err := <-s.served:
		t.Fatalf("the server stopped: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	resp, err := s.do(http.MethodDelete, "/messages")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	s.awaitLifecycle(httpapi.LifecycleReady)
	assert.Equal(t, "", s.factory.Last().ResumeRef, "a new session")

	cancel()
	select {
	case err := <-s.served:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the server didn't stop")
	}
}

// While the agent is started again after an exit, the lifecycle says it
// restarts; then the agent is ready on its session.
func TestServeRestartsTheNativeAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	s := serveNative(ctx, t, &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.Ref = "session-1"
			return
		}
		a.StartBlock = release
	}}, nil)
	s.awaitLifecycle(httpapi.LifecycleReady)

	s.factory.Last().Exit(errors.New("exit status 1"))
	require.Eventually(t, func() bool {
		a := s.factory.Last()
		return a.ResumeRef == "session-1" && a.Sink() != nil
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, httpapi.LifecycleStarting, s.lifecycle())
	close(release)
	s.awaitLifecycle(httpapi.LifecycleReady)
}
