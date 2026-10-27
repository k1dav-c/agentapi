package httpapi

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/coder/agentapi/lib/registry"
)

// instanceRegistry keeps this AgentAPI's entry in the registry directory
// while it serves, for clients that find the AgentAPIs of a machine there.
type instanceRegistry struct {
	dir    string
	logger *slog.Logger
	// entry describes this AgentAPI listening on port.
	entry func(port int) registry.Entry

	mu      sync.Mutex
	port    int    // the port it listens on, 0 until it does
	path    string // the entry's file, empty until written
	stopped bool
}

// listening writes the entry once the server listens on addr.
func (r *instanceRegistry) listening(addr net.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if tcp, ok := addr.(*net.TCPAddr); ok {
		r.port = tcp.Port
	}
	r.writeLocked()
}

// changed writes the entry again after the instance changed.
func (r *instanceRegistry) changed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || r.path == "" {
		return
	}
	r.writeLocked()
}

func (r *instanceRegistry) writeLocked() {
	path, err := registry.Write(r.dir, r.entry(r.port))
	if err != nil {
		r.logger.Warn("Failed to register this AgentAPI", "dir", r.dir, "error", err)
		return
	}
	if r.path == "" {
		r.logger.Info("Registered this AgentAPI", "path", path)
	}
	r.path = path
}

// stop removes the entry; it isn't written again.
func (r *instanceRegistry) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	if r.path != "" {
		if err := os.Remove(r.path); err != nil && !os.IsNotExist(err) {
			r.logger.Warn("Failed to remove the registry entry", "path", r.path, "error", err)
		}
		r.path = ""
	}
}

// newInstanceRegistry returns the registry of s in dir, nil without one.
func (s *Server) newInstanceRegistry(dir string, bind string) *instanceRegistry {
	if dir == "" {
		return nil
	}
	started := s.clock.Now().UTC()
	return &instanceRegistry{
		dir:    dir,
		logger: s.logger,
		entry: func(port int) registry.Entry {
			instance := s.webhook.instanceSnapshot()
			if instance.URL == "" {
				instance.URL = localURL(bind, port)
			}
			instance.Port = port
			instance.PID = os.Getpid()
			return registry.Entry{WebhookInstance: instance, AuthRequired: s.auth != nil, StartedAt: started}
		},
	}
}

// localURL is the URL of a server that listens on bind and port, through
// localhost when it listens on all interfaces.
func localURL(bind string, port int) string {
	host := bind
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}
