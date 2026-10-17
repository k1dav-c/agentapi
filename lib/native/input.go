package native

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// fileMention matches the @"<path>" references the chat UI adds for
// attachments.
var fileMention = regexp.MustCompile(`@"([^"\n]+)"`)

// MaxImageBytes is the largest image file read for an agent or for
// display; larger files attached to a message stay plain path mentions.
const MaxImageBytes = 10 << 20

// InputFromText makes an Input from message text. Mentioned files that are
// images are attached as images as well; the text keeps the mentions so the
// agent also knows the paths. Image paths are made absolute, so an agent
// with another working directory reads the same file.
func InputFromText(text string) Input {
	input := Input{Text: text}
	seen := map[string]bool{}
	for _, m := range fileMention.FindAllStringSubmatch(text, -1) {
		path := m[1]
		if seen[path] {
			continue
		}
		seen[path] = true
		if mediaType := imageMediaType(path); mediaType != "" {
			if abs, err := filepath.Abs(path); err == nil {
				path = abs
			}
			input.Images = append(input.Images, Image{Path: path, MediaType: mediaType})
		}
	}
	return input
}

// imageMediaType returns the media type of an image file judged by its
// content, or "" if path is not a readable image agents accept.
func imageMediaType(path string) string {
	f, err := openImage(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return sniffImage(head[:n])
}

// ReadImage reads an image file for an agent or for display. It fails for
// files larger than MaxImageBytes and for anything but PNG, JPEG, GIF and
// WebP images, the formats every agent accepts.
func ReadImage(path string) (data []byte, mediaType string, err error) {
	f, err := openImage(path)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	data, err = io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image: %w", err)
	}
	if len(data) > MaxImageBytes {
		return nil, "", fmt.Errorf("image %s is larger than %d MB", path, MaxImageBytes>>20)
	}
	if mediaType = sniffImage(data); mediaType == "" {
		return nil, "", fmt.Errorf("%s is not a PNG, JPEG, GIF or WebP image", path)
	}
	return data, mediaType, nil
}

// Base64 reads the image file and encodes it for the agent's protocol.
func (img Image) Base64() (string, error) {
	data, _, err := ReadImage(img.Path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// openImage opens path if it is a regular file of at most MaxImageBytes.
func openImage(path string) (*os.File, error) {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return nil, fmt.Errorf("read image: %w", err)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("image %s is not a regular file", path)
	case info.Size() > MaxImageBytes:
		return nil, fmt.Errorf("image %s is larger than %d MB", path, MaxImageBytes>>20)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	return f, nil
}

// sniffImage returns the media type of image data agents accept, or "".
// Other images (BMP, ICO) are not accepted by every agent.
func sniffImage(data []byte) string {
	switch mediaType := http.DetectContentType(data); mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mediaType
	}
	return ""
}
