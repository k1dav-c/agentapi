package native

import (
	"net/http"
	"os"
	"regexp"
)

// fileMention matches the @"<path>" references the chat UI adds for
// attachments.
var fileMention = regexp.MustCompile(`@"([^"\n]+)"`)

// maxImageBytes is the largest image file attached to a message; larger
// files stay plain path mentions.
const maxImageBytes = 10 << 20

// InputFromText makes an Input from message text. Mentioned files that are
// images are attached as images as well; the text keeps the mentions so the
// agent also knows the paths.
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
			input.Images = append(input.Images, Image{Path: path, MediaType: mediaType})
		}
	}
	return input
}

// imageMediaType returns the media type of an image file judged by its
// content, or "" if path is not a readable image.
func imageMediaType(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageBytes {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	// Other images (BMP, ICO) are not accepted by every agent.
	switch mediaType := http.DetectContentType(head[:n]); mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mediaType
	}
	return ""
}
