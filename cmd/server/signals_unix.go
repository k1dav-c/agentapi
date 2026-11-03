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

// listenForRestart catches SIGUSR2 from startup on, until the process ends
// or runs its program again: its default action ends the process, and
// agentapi restart, or a second SIGUSR2, can send it while the server
// starts or stops. One caught before serve handles it restarts the server
// once it serves; one caught while it stops is dropped.
func (s *selfExec) listenForRestart() {
	s.restartSignals = make(chan os.Signal, 1)
	signal.Notify(s.restartSignals, syscall.SIGUSR2)
}

// handleSignals sets up signal handlers for:
// - SIGTERM, SIGINT, SIGHUP: trigger graceful shutdown by canceling the context
// - SIGUSR1: write what the state database hasn't written yet, without exiting
// - SIGUSR2: restart in place: once self's program runs, stop as for a
// shutdown, after which the server runs it again (see selfExec.restart)
func handleSignals(ctx context.Context, cancel context.CancelFunc, logger *slog.Logger, srv *httpapi.Server, self *selfExec) {
	// Handle shutdown signals (SIGTERM, SIGINT, SIGHUP)
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		defer signal.Stop(shutdownCh)
		sig := <-shutdownCh
		logger.Info("Received shutdown signal", "signal", sig)
		// It wins over a restart under way.
		self.shutdown(cancel)
	}()

	if self != nil {
		// Without listenForRestart (tests), SIGUSR2 is caught while the
		// server serves.
		restartCh, owned := self.restartSignals, false
		if restartCh == nil {
			restartCh, owned = make(chan os.Signal, 1), true
			signal.Notify(restartCh, syscall.SIGUSR2)
		}
		go func() {
			if owned {
				defer signal.Stop(restartCh)
			}
			for {
				select {
				case <-restartCh:
					err := self.restart(ctx, "SIGUSR2")
					if ctx.Err() != nil {
						// Restarting, or stopping already.
						return
					}
					if err != nil {
						logger.Error("Received SIGUSR2, but can't restart in place; still serving", "error", err)
					}
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
