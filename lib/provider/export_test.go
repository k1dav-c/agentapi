package provider

import "time"

// SetNow sets the clock of s's model lists.
func (s *Source) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}
