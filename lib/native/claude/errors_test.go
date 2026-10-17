package claude

import "errors"

var (
	errSignalKilled = errors.New("signal: killed")
	errExit1        = errors.New("exit status 1")
)
