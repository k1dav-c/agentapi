package restart

import "errors"

// processOf can't describe processes on Windows, where AgentAPI can't
// restart in place.
func processOf(int) (process, error) {
	return process{}, errors.New("restarting in place is not supported on Windows")
}
