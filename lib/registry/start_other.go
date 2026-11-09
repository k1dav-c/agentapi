//go:build !linux && !darwin

package registry

// processStart can't tell here: List checks only that the PID runs.
func processStart(int) string { return "" }
