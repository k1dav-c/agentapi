package agentlog

import (
	"path/filepath"
	"sync"
)

// Dir is the working directory an agent runs in, as a log tells it. The
// agents record its real path, which they get from the operating system,
// while AgentAPI may run in a path through a symbolic link: Go's os.Getwd
// returns $PWD when it names the directory, and so the path the user
// went through. It is safe for concurrent use.
type Dir struct {
	path, real string

	mu sync.Mutex
	// is remembers what Is said of the paths logs recorded: resolving
	// one takes a system call per element, and Codex keeps every
	// directory's logs in one tree.
	is map[string]bool
}

// maxDirMemo bounds how many recorded paths a Dir remembers.
const maxDirMemo = 4096

// NewDir returns the directory at path.
func NewDir(path string) *Dir {
	return &Dir{path: filepath.Clean(path), real: RealPath(path), is: make(map[string]bool)}
}

// RealPath is path with its symbolic links resolved, or cleaned when they
// can't be (it doesn't exist).
func RealPath(path string) string {
	clean := filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return real
	}
	return clean
}

// Path is the directory as AgentAPI was given it.
func (d *Dir) Path() string { return d.path }

// Paths are the directory's real path and, when it differs, the path
// AgentAPI was given: the agents name the directories they keep logs in
// after the path they record, the real one.
func (d *Dir) Paths() []string {
	if d.path == d.real {
		return []string{d.path}
	}
	return []string{d.real, d.path}
}

// Is reports whether a working directory a log records is d, whichever
// path to it either went through.
func (d *Dir) Is(recorded string) bool {
	if recorded == "" {
		return false
	}
	clean := filepath.Clean(recorded)
	if clean == d.path || clean == d.real {
		return true
	}
	d.mu.Lock()
	is, ok := d.is[clean]
	d.mu.Unlock()
	if ok {
		return is
	}
	is = RealPath(clean) == d.real
	d.mu.Lock()
	if len(d.is) >= maxDirMemo {
		clear(d.is)
	}
	d.is[clean] = is
	d.mu.Unlock()
	return is
}
