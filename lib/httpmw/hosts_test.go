package httpmw_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpmw"
)

func TestParseBindAddress(t *testing.T) {
	t.Parallel()
	valid := map[string]string{
		"":            "",
		"127.0.0.1":   "127.0.0.1",
		"0.0.0.0":     "0.0.0.0",
		"::1":         "::1",
		"[::1]":       "::1",
		"::":          "::",
		"fe80::1":     "fe80::1",
		"localhost":   "localhost",
		"my-host.lan": "my-host.lan",
		" 10.0.0.2 ":  "10.0.0.2",
	}
	for input, want := range valid {
		got, err := httpmw.ParseBindAddress(input)
		require.NoError(t, err, "input %q", input)
		assert.Equal(t, want, got, "input %q", input)
	}
	for _, input := range []string{
		"127.0.0.1:3284",
		"localhost:80",
		"[::1]:3284",
		"[localhost]",
		"http://localhost",
		"local host",
		"a,b",
		"-bad.example",
		"under_score",
	} {
		_, err := httpmw.ParseBindAddress(input)
		assert.Error(t, err, "input %q", input)
	}
}
