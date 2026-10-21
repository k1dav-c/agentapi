//go:build !linux

package jsonlwatcher

import "time"

// birthTime is when the file at path was created; elsewhere than Linux it
// isn't read.
func birthTime(string) (time.Time, bool) { return time.Time{}, false }
