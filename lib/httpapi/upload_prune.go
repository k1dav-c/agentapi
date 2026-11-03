package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"time"
)

// Unused uploads are removed: a file the conversation (its messages, rich
// messages, timeline, requests and queue) doesn't refer to, by the
// checksum in its path or URL, once it is older than uploadGrace, which
// covers the drafts a browser keeps of a message being written. They are
// looked for on start, every uploadPruneInterval, and after the
// conversation was cleared.
const (
	uploadGrace         = 7 * 24 * time.Hour
	uploadPruneInterval = time.Hour
)

// pruneCandidates returns the checksum directories that are old enough to
// be removed, should nothing refer to them. Only what save makes counts:
// a real directory named by a checksum, holding regular files, none of
// them, nor the directory, changed since cutoff. Symlinks are never
// followed; anything else in the directory is left alone.
func (u *uploadStore) pruneCandidates(cutoff time.Time) ([]string, error) {
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for checksum, at := range u.saved {
		if at.Before(cutoff) {
			delete(u.saved, checksum)
		}
	}
	var candidates []string
	for _, entry := range entries {
		checksum := entry.Name()
		if !entry.IsDir() || !uploadChecksumRe.MatchString(checksum) {
			continue
		}
		if _, ok := u.unusedFilesLocked(root, checksum, cutoff); ok {
			candidates = append(candidates, checksum)
		}
	}
	return candidates, nil
}

// unusedFilesLocked returns the files in the checksum directory, and
// whether it may be removed as of cutoff (see pruneCandidates). Caller
// holds u.mu.
func (u *uploadStore) unusedFilesLocked(root *os.Root, checksum string, cutoff time.Time) ([]fs.FileInfo, bool) {
	if at, ok := u.saved[checksum]; ok && !at.Before(cutoff) {
		return nil, false
	}
	info, err := root.Lstat(checksum)
	if err != nil || !info.IsDir() || !info.ModTime().Before(cutoff) {
		return nil, false
	}
	entries, err := fs.ReadDir(root.FS(), checksum)
	if err != nil {
		return nil, false
	}
	files := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := root.Lstat(path.Join(checksum, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			return nil, false
		}
		files = append(files, info)
	}
	return files, true
}

// prune removes the candidate directories that unused reports unused and
// that are still old enough, and returns how many files it removed and
// their size.
func (u *uploadStore) prune(candidates []string, unused func(checksum string) bool, cutoff time.Time) (int, int64, error) {
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = root.Close() }()
	u.mu.Lock()
	defer u.mu.Unlock()
	count, size := 0, int64(0)
	var errs []error
	for _, checksum := range candidates {
		if !unused(checksum) {
			continue
		}
		// Checked again: a file may have been stored there since.
		files, ok := u.unusedFilesLocked(root, checksum, cutoff)
		if !ok {
			continue
		}
		for _, file := range files {
			if err := root.Remove(path.Join(checksum, file.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				continue
			}
			count++
			size += file.Size()
		}
		if err := root.Remove(checksum); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return count, size, errors.Join(errs...)
}

// uploadRefs finds the checksums of stored files in what the conversation
// holds. A checksum is 16 hex digits; any run of them that contains one
// counts, so a reference is never missed for how it is written (a path, a
// URL, JSON).
type uploadRefs struct {
	candidates map[string]bool
	found      map[string]bool
}

func newUploadRefs(candidates []string) *uploadRefs {
	r := &uploadRefs{candidates: make(map[string]bool, len(candidates)), found: make(map[string]bool)}
	for _, c := range candidates {
		r.candidates[c] = true
	}
	return r
}

// scan notes the candidates text refers to.
func (r *uploadRefs) scan(text string) {
	const n = 16
	run := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if '0' <= c && c <= '9' || 'a' <= c && c <= 'f' {
			run++
			if run >= n {
				if s := text[i+1-n : i+1]; r.candidates[s] {
					r.found[s] = true
				}
			}
			continue
		}
		run = 0
	}
}

// scanJSON notes the candidates v refers to, encoded as JSON.
func (r *uploadRefs) scanJSON(v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return false
	}
	r.scan(string(data))
	return true
}

// startUploadPruning removes unused uploads now, every
// uploadPruneInterval and when asked to (requestUploadPrune), until the
// server stops.
func (s *Server) startUploadPruning() {
	s.uploadPruneNow = make(chan struct{}, 1)
	s.uploadPruneNow <- struct{}{}
	s.uploadPruneDone = make(chan struct{})
	go func() {
		defer close(s.uploadPruneDone)
		ticker := time.NewTicker(uploadPruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.shutdownCtx.Done():
				return
			case <-ticker.C:
			case <-s.uploadPruneNow:
			}
			s.pruneUploads(time.Now())
		}
	}()
}

// requestUploadPrune has the unused uploads removed soon, such as those of
// a conversation that was cleared.
func (s *Server) requestUploadPrune() {
	if s.uploadPruneNow == nil {
		return
	}
	select {
	case s.uploadPruneNow <- struct{}{}:
	default:
	}
}

// pruneUploads removes the uploads the conversation doesn't refer to that
// are older than uploadGrace as of now.
func (s *Server) pruneUploads(now time.Time) {
	cutoff := now.Add(-uploadGrace)
	candidates, err := s.uploads.pruneCandidates(cutoff)
	if err != nil {
		s.logger.Warn("Failed to look for unused uploads", "uploadDir", s.uploads.dir, "error", err)
		return
	}
	if len(candidates) == 0 {
		return
	}
	refs := newUploadRefs(candidates)
	if !s.scanUploadRefs(refs) {
		// Unsure what the conversation refers to: keep everything.
		s.logger.Warn("Failed to read the conversation for unused uploads; keeping them")
		return
	}
	count, size, err := s.uploads.prune(candidates, func(checksum string) bool { return !refs.found[checksum] }, cutoff)
	if err != nil {
		s.logger.Warn("Failed to remove unused uploads", "uploadDir", s.uploads.dir, "error", err)
	}
	if count > 0 {
		s.logger.Info("Removed unused uploads", "uploadDir", s.uploads.dir, "files", count, "bytes", size)
	}
}

// scanUploadRefs notes the uploads the conversation refers to, and reports
// false if it couldn't read all of it. It holds s.mu, under which a
// conversation is replaced (a restart, a switch of sessions) in one step.
func (s *Server) scanUploadRefs(refs *uploadRefs) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The queue before the messages: a message that leaves the queue in
	// between is in the messages then.
	ok := refs.scanJSON(s.queue.Messages())
	for _, msg := range s.conversation.Messages() {
		refs.scan(msg.Message)
	}
	ok = refs.scanJSON(s.emitter.RichMessages()) && ok
	ok = refs.scanJSON(s.emitter.SessionEvents()) && ok
	return refs.scanJSON(s.emitter.Requests()) && ok
}
