package hub

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A browser that stops reading its stream ends it: the handler doesn't wait
// forever for the write.
func TestStreamEndsWhenTheBrowserStopsReading(t *testing.T) {
	h, err := New(Config{NoToken: true, Secret: "s"})
	require.NoError(t, err)
	h.streamWriteTimeout = 200 * time.Millisecond
	ended := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(ended)
		h.streamEvents(w, r)
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.(*net.TCPConn).SetReadBuffer(4<<10))
	_, err = fmt.Fprintf(conn, "GET /api/events HTTP/1.1\r\nHost: %s\r\n\r\n", srv.Listener.Addr())
	require.NoError(t, err)
	// Changes larger than the connection's buffers, which nobody reads.
	text := strings.Repeat("x", 1<<20)
	for i := range 64 {
		_, err := h.store.updateSettings(func(s *hubSettings) error {
			s.Favorites = []Favorite{{ID: "f", Text: fmt.Sprint(i, text)}}
			return nil
		})
		require.NoError(t, err)
		select {
		case <-ended:
			return
		default:
		}
	}
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream didn't end")
	}
}
