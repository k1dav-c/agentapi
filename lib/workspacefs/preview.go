package workspacefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/agentapi/lib/htmlpreview"
)

// MaxPreviewPage is how large an HTML file Preview shows.
const MaxPreviewPage = 4 << 20

// Preview returns the HTML file at path as a page that shows by itself,
// with what it refers to by relative path inlined (htmlpreview), and its
// path relative to the directory, with slashes.
func (d Dir) Preview(path string) (htmlpreview.Page, string, error) {
	rel, err := d.Resolve(path)
	if err != nil {
		return htmlpreview.Page{}, "", err
	}
	if ext := strings.ToLower(filepath.Ext(rel)); ext != ".html" && ext != ".htm" {
		return htmlpreview.Page{}, "", refuse(Invalid, fmt.Sprintf("%s isn't an HTML file", path))
	}
	// The root keeps the page and what it refers to inside the working
	// directory, symlinks included.
	root, err := os.OpenRoot(string(d))
	if err != nil {
		return htmlpreview.Page{}, "", fmt.Errorf("open the agent's working directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	data, _, err := readRegular(root, rel, path, MaxPreviewPage)
	if err != nil {
		return htmlpreview.Page{}, "", err
	}
	if len(data) > MaxPreviewPage {
		return htmlpreview.Page{}, "", refuse(Invalid, fmt.Sprintf("%s is larger than %d MiB, too large to preview", path, MaxPreviewPage>>20))
	}
	page, err := htmlpreview.Inline(root.FS(), filepath.ToSlash(rel), data)
	if err != nil {
		return htmlpreview.Page{}, "", fmt.Errorf("preview %s: %w", path, err)
	}
	return page, filepath.ToSlash(rel), nil
}

// ErrNoRoot is the error Open returns when the directory itself can't be
// opened.
var ErrNoRoot = errors.New("the agent's working directory can't be opened")

// Open opens the regular file at path, for serving it. It is checked
// before it is opened: opening a FIFO would wait for a writer.
func (d Dir) Open(path string) (*os.File, fs.FileInfo, error) {
	rel, err := d.Resolve(path)
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(string(d))
	if err != nil {
		return nil, nil, ErrNoRoot
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(rel)
	if err != nil {
		return nil, nil, fileError(path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, refuse(Invalid, path+" is not a file")
	}
	file, err := root.Open(rel)
	if err != nil {
		return nil, nil, fileError(path, err)
	}
	return file, info, nil
}
