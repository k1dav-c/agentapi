package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// persistInterval is how long the hub gathers changes before it writes a
// file: a summary stream that changes many times a second costs one write,
// not one per change.
const persistInterval = time.Second

// persister writes a file in the background: a change marks it, and it
// writes what snapshot returns at most once per persistInterval, and once
// more on close. A write that fails is logged, and tried again with the
// next change.
type persister struct {
	path     string
	what     string // what the file keeps, for the log
	snapshot func() ([]byte, error)
	logger   *slog.Logger

	// writeMu serializes the writes: the background one and flush.
	writeMu sync.Mutex
	mu      sync.Mutex
	pending bool
	failing bool

	nudge     chan struct{}
	done      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once
}

func newPersister(path, what string, logger *slog.Logger, snapshot func() ([]byte, error)) *persister {
	p := &persister{
		path: path, what: what, snapshot: snapshot, logger: logger,
		nudge: make(chan struct{}, 1), done: make(chan struct{}), stopped: make(chan struct{}),
	}
	go p.run()
	return p
}

// mark says the file is behind; it is written within persistInterval.
func (p *persister) mark() {
	p.mu.Lock()
	p.pending = true
	p.mu.Unlock()
	select {
	case p.nudge <- struct{}{}:
	default:
	}
}

func (p *persister) run() {
	defer close(p.stopped)
	for {
		select {
		case <-p.nudge:
		case <-p.done:
			return
		}
		timer := time.NewTimer(persistInterval)
		select {
		case <-timer.C:
		case <-p.done:
			timer.Stop()
		}
		_ = p.flush()
	}
}

// flush writes the file now if a change is pending.
func (p *persister) flush() error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.mu.Lock()
	pending := p.pending
	p.pending = false
	p.mu.Unlock()
	if !pending {
		return nil
	}
	data, err := p.snapshot()
	if err == nil {
		err = writeFileAtomic(p.path, data)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		// Tried again with the next change, or on close.
		p.pending = true
		if !p.failing {
			p.logger.Warn("Failed to keep the hub's "+p.what, "file", p.path, "error", err)
		}
		p.failing = true
		return err
	}
	if p.failing {
		p.logger.Info("Kept the hub's "+p.what+" again", "file", p.path)
	}
	p.failing = false
	return nil
}

// close stops the background writes and writes what is pending.
func (p *persister) close() error {
	p.closeOnce.Do(func() { close(p.done) })
	<-p.stopped
	return p.flush()
}

// writeFileAtomic writes data to path, readable by its owner only, through
// a file renamed into place: path is the old data or the new, never part
// of either.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// On disk before it takes the file's name: after a crash, the name
	// would otherwise point at an empty file.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir puts the entries of dir, such as a file renamed in it, on disk,
// where the system can sync a directory.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// stateVersion is the version of the file of instances this hub writes.
// A hub of 2.2 or earlier wrote a bare array of instances, which this one reads
// too; it reads the envelope as damaged (see readState).
const stateVersion = 1

// stateFile is the file of instances.
type stateFile struct {
	Version   int              `json:"version"`
	Instances []storedInstance `json:"instances"`
}

// errDamaged is a file of instances that doesn't parse.
var errDamaged = errors.New("damaged")

// newerStateError is a file a newer hub wrote, which this one doesn't read
// rather than take for damaged and start without its instances.
type newerStateError struct {
	path    string
	version int
}

func (e newerStateError) Error() string {
	return fmt.Sprintf("%s is of a newer hub (file version %d; this hub reads %d): run that hub, or start from %s.bak", e.path, e.version, stateVersion, e.path)
}

// decodeState reads the file of instances: the envelope, or the bare array
// of a hub before it.
func decodeState(path string, data []byte) ([]storedInstance, error) {
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		var stored []storedInstance
		if err := json.Unmarshal(trimmed, &stored); err != nil {
			return nil, fmt.Errorf("%w: %w", errDamaged, err)
		}
		return stored, nil
	}
	var file stateFile
	if err := json.Unmarshal(trimmed, &file); err != nil {
		return nil, fmt.Errorf("%w: %w", errDamaged, err)
	}
	switch {
	case file.Version > stateVersion:
		return nil, newerStateError{path: path, version: file.Version}
	case file.Version < 1:
		return nil, fmt.Errorf("%w: no file version", errDamaged)
	}
	return file.Instances, nil
}

// encodeState is the file of instances.
func encodeState(stored []storedInstance) ([]byte, error) {
	if stored == nil {
		stored = []storedInstance{}
	}
	return json.MarshalIndent(stateFile{Version: stateVersion, Instances: stored}, "", "  ")
}

// readState returns the instances kept in path, and keeps a copy of the
// file as it was in path.bak. A file that doesn't parse is moved aside and
// the hub starts empty: a hub that stopped on it would stop again at every
// restart. One a newer hub wrote is an error: the hub doesn't start rather
// than lose its instances.
func readState(path string, logger *slog.Logger) ([]storedInstance, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	stored, err := decodeState(path, data)
	var newer newerStateError
	switch {
	case errors.As(err, &newer):
		return nil, err
	case err != nil:
		aside := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if renameErr := os.Rename(path, aside); renameErr != nil {
			return nil, fmt.Errorf("read %s: %w; moving it aside failed: %w", path, err, renameErr)
		}
		logger.Warn("The hub's file of instances doesn't parse; moved it aside and starting empty. "+
			"Lost: the instances added by hand, with their tokens, and every instance's group, pin and mute; joined instances come back with their next heartbeat. "+
			"The file as an earlier start of the hub found it is in the .bak next to it",
			"file", path, "moved_to", aside, "error", err)
		return nil, nil
	}
	// The last file that parsed, for a change that went wrong (an edit by
	// hand, an older hub that set it aside).
	if err := writeFileAtomic(path+".bak", data); err != nil {
		logger.Warn("Failed to keep a copy of the hub's file of instances", "file", path+".bak", "error", err)
	}
	return stored, nil
}
