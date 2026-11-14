package provider

import "time"

// SetNow sets the clock of s's model lists.
func (s *Source) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// OpenRaw decrypts data, what Seal sealed decoded from base64, into its
// JSON.
func OpenRaw(secret string, data []byte) ([]byte, error) {
	aead, err := newAEAD(secret)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
}
