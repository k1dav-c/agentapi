package hub

import "errors"

// Flush writes what the hub's files are behind on now, for a test that
// reads them, or starts another hub on them, while the hub runs.
func (h *Hub) Flush() error {
	return errors.Join(h.store.flush(), h.deliveries.flush())
}
