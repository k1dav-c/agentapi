package agentlog

import (
	"os"
	"time"
)

// FileInfo is what Stat says of a session log.
type FileInfo struct {
	Size    int64
	ModTime time.Time
	// Birth is when the file was created, where the file system records
	// it; zero elsewhere.
	Birth time.Time
	sys   os.FileInfo
}

// Stat describes the file at path.
func Stat(path string) (FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	birth, _ := BirthTime(path)
	return FileInfo{Size: info.Size(), ModTime: info.ModTime(), Birth: birth, sys: info}, nil
}

// SameFile reports whether f and o describe the same file rather than one
// that replaced it at its path.
func (f FileInfo) SameFile(o FileInfo) bool {
	if f.sys == nil || o.sys == nil || !os.SameFile(f.sys, o.sys) {
		return false
	}
	// An inode can be reused once its file was removed: a creation time
	// tells the new file apart.
	return f.Birth.IsZero() || o.Birth.IsZero() || f.Birth.Equal(o.Birth)
}
