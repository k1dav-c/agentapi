package httpapi

import (
	"bytes"
	"crypto/rand"
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
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// uploadStore is the directory uploads and tool result images are kept
// in, as <checksum>/<name>: checksum is the first 8 bytes of the content's
// SHA-256, in hex, so a file at a path never changes.
type uploadStore struct {
	dir string
	// keep: the directory outlives the server, with the conversation that
	// refers to its files by path.
	keep bool
	// mu keeps prune from removing a directory save is storing a file in.
	mu sync.Mutex
	// saved is when each checksum directory was last saved to, in this
	// run: a file that was there already isn't written again, and is new
	// to whoever is about to refer to it. Guarded by mu.
	saved map[string]time.Time
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

// maxUploadNameLength is the longest name a file is stored under, in
// bytes: most file systems take 255.
const maxUploadNameLength = 255

// uploadName is the name an uploaded file is stored under: the base name
// of the one the browser sent, without control characters (a newline
// would break the path the agent is given), cut to maxUploadNameLength
// with its extension kept. It reports false for a name that names no file
// ("", ".", "..").
func uploadName(sent string) (string, bool) {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, sent)
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if len(name) > maxUploadNameLength {
		ext := path.Ext(name)
		if len(ext) > maxUploadNameLength/2 {
			ext = ""
		}
		stem := name[:maxUploadNameLength-len(ext)]
		for !utf8.ValidString(stem) {
			stem = stem[:len(stem)-1]
		}
		name = stem + ext
	}
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", false
	}
	return name, true
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

// errUploadTooLarge is a file saveFrom refused for its size.
var errUploadTooLarge = errors.New("the file is too large")

// save stores data as name in the directory of its checksum, and returns
// its path on disk and its checksum (saveFrom).
func (u *uploadStore) save(name string, data []byte) (string, string, error) {
	return u.saveFrom(name, bytes.NewReader(data), int64(len(data)))
}

// saveFrom stores what r reads as name in the directory of its checksum,
// and returns its path on disk and its checksum; more than limit bytes is
// errUploadTooLarge. The content goes to a temporary file in the
// directory as it is read, never all in memory, and is renamed into
// place. A file of the same size already there is this content (a tool
// result's image comes again with every snapshot of its message).
func (u *uploadStore) saveFrom(name string, r io.Reader, limit int64) (string, string, error) {
	// A name is one file in the directory.
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || name != filepath.Base(name) {
		return "", "", fmt.Errorf("invalid file name %q", name)
	}
	root, err := os.OpenRoot(u.dir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = root.Close() }()
	temp, size, hash, err := writeRootTemp(root, io.LimitReader(r, limit+1))
	if err != nil {
		return "", "", err
	}
	// Until it is renamed into place.
	defer func() { _ = root.Remove(temp) }()
	if size > limit {
		return "", "", errUploadTooLarge
	}
	checksum := hex.EncodeToString(hash[:8])
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.saved == nil {
		u.saved = make(map[string]time.Time)
	}
	u.saved[checksum] = time.Now()
	if err := root.Mkdir(checksum, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", "", err
	}
	file := path.Join(checksum, name)
	if info, err := root.Lstat(file); err == nil && info.Mode().IsRegular() && info.Size() == size {
		return filepath.Join(u.dir, checksum, name), checksum, nil
	}
	if err := root.Rename(temp, file); err != nil {
		return "", "", err
	}
	return filepath.Join(u.dir, checksum, name), checksum, nil
}

// writeRootTemp copies r to a new temporary file in root, and returns its
// name, size and SHA-256.
func writeRootTemp(root *os.Root, r io.Reader) (string, int64, [sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", 0, sum, err
	}
	name := ".upload-" + hex.EncodeToString(suffix[:])
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", 0, sum, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, hash), r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = root.Remove(name)
		return "", 0, sum, err
	}
	copy(sum[:], hash.Sum(nil))
	return name, size, sum, nil
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
