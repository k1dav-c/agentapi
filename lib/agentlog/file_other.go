//go:build !linux

package agentlog

import "time"

// BirthTime is when the file at path was created; elsewhere than Linux it
// isn't read.
func BirthTime(string) (time.Time, bool) { return time.Time{}, false }
