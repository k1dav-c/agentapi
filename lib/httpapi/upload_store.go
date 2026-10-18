package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

// uploadStore is the directory uploads and tool result images are kept
// in, as <checksum>/<name>: checksum is the first 8 bytes of the content's
// SHA-256, in hex, so a file at a path never changes.
type uploadStore struct {
	dir string
	// keep: the directory outlives the server, with the conversation that
	// refers to its files by path.
	keep bool
}

// uploadChecksumRe matches the directory a file is stored in.
var uploadChecksumRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// previewableImageTypes are the stored files GET /uploads serves, by
// sniffed content type. SVG is left out (it can carry script), as is
// everything that isn't an image.
var previewableImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/bmp":  true,
}

// imageExtensions names a stored image by its sniffed content type.
var imageExtensions = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
	"image/bmp":  "bmp",
}

// newUploadStore uses dir, created if needed and kept, or a temporary
// directory that remove deletes.
func newUploadStore(dir string) (*uploadStore, error) {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("failed to create upload directory: %w", err)
		}
		return &uploadStore{dir: dir, keep: true}, nil
	}
	dir, err := os.MkdirTemp("", "agentapi-uploads-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	return &uploadStore{dir: dir}, nil
}

// save stores data as name in the directory of its checksum, and returns
// its path on disk and its checksum. A file of the same size already there
// is this content (a tool result's image comes again with every snapshot
// of its message).
func (u *uploadStore) save(name string, data []byte) (string, string, error) {
	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:8])
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = root.Close() }()
	if err := root.Mkdir(checksum, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", "", err
	}
	file := path.Join(checksum, name)
	if info, err := root.Stat(file); err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
		if err := writeRootFile(root, file, data); err != nil {
			return "", "", err
		}
	}
	return filepath.Join(u.dir, checksum, name), checksum, nil
}

// writeRootFile writes data to the file at name in root.
func writeRootFile(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// saveImage stores a previewable image and returns the path GET /uploads
// serves it at.
func (u *uploadStore) saveImage(data []byte) (string, error) {
	contentType := http.DetectContentType(data)
	if !previewableImageTypes[contentType] {
		return "", fmt.Errorf("not a previewable image: %s", contentType)
	}
	name := "image." + imageExtensions[contentType]
	_, checksum, err := u.save(name, data)
	if err != nil {
		return "", err
	}
	return "/uploads/" + checksum + "/" + name, nil
}

// serve writes the image stored as checksum/name, or an error status.
//
// The chat can tell the agent to run commands, so whatever this serves
// runs with that power if it is ever rendered as a page: it only reads the
// store's directory (through os.Root, so symlinks can't lead out of it),
// only serves files whose content is a raster image, and tells the browser
// not to sniff or run anything.
func (u *uploadStore) serve(w http.ResponseWriter, r *http.Request, checksum, name string) {
	if !uploadChecksumRe.MatchString(checksum) || name == "" || name != path.Base(name) || name == "." || name == ".." {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(path.Join(checksum, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}
	contentType := http.DetectContentType(head[:n])
	if !previewableImageTypes[contentType] {
		http.Error(w, "only uploaded images can be previewed", http.StatusUnsupportedMediaType)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// The directory name is the content's checksum, so the file at a URL
	// doesn't change.
	header.Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// remove deletes the directory and everything in it, unless it is kept.
// It reports whether it removed it.
func (u *uploadStore) remove() (bool, error) {
	if u.keep {
		return false, nil
	}
	return true, os.RemoveAll(u.dir)
}
