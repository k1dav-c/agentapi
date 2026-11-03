package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// A summary whose data spans several data: lines is read whole, as the
// SSE standard joins them, not only its last line.
func TestReadSummariesJoinsDataLines(t *testing.T) {
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: summary_update\ndata: {\"instance\": {\"id\": \"dev:1\"},\ndata:  \"title\": \"two lines\"}\n\n"))
	}))
	defer instance.Close()
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	h.ctx = t.Context()
	var got []instanceapi.SummaryBody
	err = h.readSummaries(t.Context(), Instance{record: record{WebhookInstance: instanceapi.WebhookInstance{ID: "dev:1", URL: instance.URL}}}, func(s instanceapi.SummaryBody) {
		got = append(got, s)
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "two lines", got[0].Title)
}
