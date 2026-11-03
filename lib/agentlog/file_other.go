//go:build !linux && !darwin

package agentlog

import "time"

// BirthTime is when the file at path was created; elsewhere than Linux
// and macOS it isn't read.
func BirthTime(string) (time.Time, bool) { return time.Time{}, false }
