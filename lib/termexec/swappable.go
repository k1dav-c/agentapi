package termexec

import "sync"

// SwappableProcess keeps AgentIO users attached while the underlying PTY
// process is replaced after a configuration change.
type SwappableProcess struct {
	mu      sync.RWMutex
	current *Process
}

func NewSwappableProcess(process *Process) *SwappableProcess {
	return &SwappableProcess{current: process}
}

func (s *SwappableProcess) Current() *Process {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *SwappableProcess) Set(process *Process) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = process
}

func (s *SwappableProcess) Write(data []byte) (int, error) {
	return s.Current().Write(data)
}

func (s *SwappableProcess) ReadScreen() string {
	return s.Current().ReadScreen()
}
