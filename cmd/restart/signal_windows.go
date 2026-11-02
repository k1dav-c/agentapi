package restart

import "errors"

// signalRestart can't signal on Windows.
func signalRestart(int) error {
	return errors.New("restarting in place is not supported on Windows")
}
