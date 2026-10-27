package httpapi

import (
	"bufio"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Text is gzipped for a client that takes it, an event stream event by
// event, and anything else goes as it is.
func TestCompress(t *testing.T) {
	handler := Compress()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"` + strings.Repeat("a", 4096) + `"}`))
		case "/png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(strings.Repeat("b", 4096)))
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: one\ndata: {}\n\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()
	get := func(path string) *http.Response {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		// Set by hand, so the client doesn't undo the encoding itself.
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	resp := get("/json")
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	_ = resp.Body.Close()
	resp = get("/png")
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	_ = resp.Body.Close()

	// The stream's first event arrives while it stays open.
	resp = get("/events")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	got := make(chan string, 1)
	go func() {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			got <- err.Error()
			return
		}
		line, _ := bufio.NewReader(zr).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		assert.Equal(t, "event: one\n", line)
	case <-time.After(5 * time.Second):
		t.Fatal("the event didn't arrive while the stream was open")
	}
}
