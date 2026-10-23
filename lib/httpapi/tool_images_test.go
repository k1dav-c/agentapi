package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/transcript"
)

func TestStoreRichImages(t *testing.T) {
	t.Parallel()
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv, err := NewServer(ctx, ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))

	msg := transcript.RichMessage{Content: []transcript.RichContentBlock{{
		Type:      "tool_result",
		ToolUseID: "toolu_img",
		Images: []transcript.RichImage{
			{MediaType: "image/png", Data: pngBytes.Bytes()},
			// An "image" whose content is markup is never stored.
			{MediaType: "image/png", Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		},
	}}}
	srv.storeRichImages(&msg)
	// The same image again (the log is re-read on restart) gets the same URL.
	again := transcript.RichMessage{Content: []transcript.RichContentBlock{{
		Type:   "tool_result",
		Images: []transcript.RichImage{{MediaType: "image/png", Data: pngBytes.Bytes()}},
	}}}
	srv.storeRichImages(&again)

	images := msg.Content[0].Images
	require.Len(t, images, 1)
	assert.Regexp(t, `^/uploads/[0-9a-f]{16}/image\.png$`, images[0].URL)
	assert.Nil(t, images[0].Data)
	assert.Equal(t, images[0].URL, again.Content[0].Images[0].URL)

	// Clients get the URL, never the bytes.
	encoded, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"data"`)
	assert.Contains(t, string(encoded), `"url":"/uploads/`)

	resp, err := http.Get(ts.URL + images[0].URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, pngBytes.Bytes(), got)
}

// A native agent's tool result images are stored before the conversation
// takes the message, and clients get their URLs.
func TestNativeRichImagesAreStored(t *testing.T) {
	srv, factory := newQuietNativeServer(t, nil)
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	factory.Last().Sink().RichMessage(transcript.RichMessage{MessageID: "u1", Role: "user", Content: []transcript.RichContentBlock{{
		Type: "tool_result", ToolUseID: "toolu_img",
		Images: []transcript.RichImage{{MediaType: "image/png", Data: pngBytes.Bytes()}},
	}}})
	rich := srv.emitter.RichMessages()
	require.Len(t, rich, 1)
	require.Len(t, rich[0].Content[0].Images, 1)
	assert.Regexp(t, `^/uploads/[0-9a-f]{16}/image\.png$`, rich[0].Content[0].Images[0].URL)
	assert.Nil(t, rich[0].Content[0].Images[0].Data)
}
