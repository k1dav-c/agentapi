package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// compressibleTypes are the responses that shrink: text, JSON, the event
// streams (flushed event by event, so they stream as before) and the chat's
// files. Images and other binaries go as they are.
var compressibleTypes = []string{
	"text/html",
	"text/css",
	"text/plain",
	"text/javascript",
	"text/event-stream",
	"application/javascript",
	"application/json",
	"application/problem+json",
	"application/manifest+json",
	"image/svg+xml",
}

// Compress gzips the responses of compressibleTypes for clients that take
// it. A long conversation's event stream and JSON are megabytes of text,
// which a phone downloads in a tenth of the bytes.
func Compress() func(http.Handler) http.Handler {
	return middleware.Compress(5, compressibleTypes...)
}
