package host

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// git's credential helper gives a job's token for its own repository
// alone: a path of another one is refused before the launcher is asked.
func TestGitCredentialPath(t *testing.T) {
	key := Key{Owner: "acme", Name: "widget", Issue: 7}
	// No launcher listens here: a request that gets past the checks fails
	// to reach it.
	socket := filepath.Join(t.TempDir(), "host.sock")
	for name, test := range map[string]struct {
		path    string
		refused bool
	}{
		"another repository":    {"acme/other.git", true},
		"another owner":         {"evil/widget", true},
		"a prefix of it":        {"acme/widgets", true},
		"its own":               {"acme/widget.git", false},
		"its own, other case":   {"ACME/Widget", false},
		"its own, with slashes": {"/acme/widget/", false},
		"no path":               {"", false},
	} {
		var out bytes.Buffer
		in := "protocol=https\nhost=github.com\n"
		if test.path != "" {
			in += "path=" + test.path + "\n"
		}
		err := GitCredential(context.Background(), "get", strings.NewReader(in+"\n"), &out, socket, key)
		if test.refused {
			assert.ErrorContains(t, err, "has no credentials for", name)
		} else {
			assert.Error(t, err, name)
			assert.NotContains(t, err.Error(), "has no credentials for", name)
		}
		assert.Empty(t, out.String(), name)
	}
}
