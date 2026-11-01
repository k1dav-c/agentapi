//go:build unix

package server

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/coder/agentapi/lib/httpapi"
)

// handleSignals sets up signal handlers for:
// - SIGTERM, SIGINT, SIGHUP: trigger graceful shutdown by canceling the context
// - SIGUSR1: write what the state database hasn't written yet, without exiting
// - SIGUSR2: restart in place: once self's program runs, stop as for a
// shutdown, after which the server runs it again (see selfExec)
func handleSignals(ctx context.Context, cancel context.CancelFunc, logger *slog.Logger, srv *httpapi.Server, self *selfExec) {
	// Handle shutdown signals (SIGTERM, SIGINT, SIGHUP)
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		defer signal.Stop(shutdownCh)
		sig := <-shutdownCh
		logger.Info("Received shutdown signal", "signal", sig)
		if self.restarting() {
			// It wins over a restart under way.
			self.requested.Store(false)
		}
		cancel()
	}()

	if self != nil {
		restartCh := make(chan os.Signal, 1)
		signal.Notify(restartCh, syscall.SIGUSR2)
		go func() {
			defer signal.Stop(restartCh)
			for {
				select {
				case <-restartCh:
					version, err := self.check(ctx)
					if ctx.Err() != nil {
						// Stopping already.
						return
					}
					if err != nil {
						logger.Error("Received SIGUSR2, but can't restart in place; still serving", "error", err)
						continue
					}
					logger.Info("Received SIGUSR2, restarting in place", "program", self.path, "version", version)
					self.requested.Store(true)
					cancel()
					return
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Handle SIGUSR1 for save without exit
	saveOnlyCh := make(chan os.Signal, 1)
	signal.Notify(saveOnlyCh, syscall.SIGUSR1)
	go func() {
		defer signal.Stop(saveOnlyCh)
		for {
			select {
			case <-saveOnlyCh:
				logger.Info("Received SIGUSR1, saving state without exiting")
				if err := srv.SaveState("SIGUSR1"); err != nil {
					logger.Error("Failed to save state on SIGUSR1", "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
