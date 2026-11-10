package hub

import "errors"

// Flush writes what the hub's files are behind on now, for a test that
// reads them, or starts another hub on them, while the hub runs.
func (h *Hub) Flush() error {
	return errors.Join(h.store.flush(), h.deliveries.flush())
}

// flush writes what the file is behind on now.
func (s *store) flush() error {
	if s.persist == nil {
		return nil
	}
	return errors.Join(s.persist.flush(), s.activity.flush())
}

func (d *deliveries) flush() error {
	if d.persist == nil {
		return nil
	}
	return d.persist.flush()
}
