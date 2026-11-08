package hub_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/hub"
)

// hub-schema.json is the hub's schema as make gen writes it; the page's
// types are generated from it (bun run gen:api in chat/).
func TestHubSchema(t *testing.T) {
	t.Parallel()
	generated, err := hub.Schema()
	require.NoError(t, err)
	committed, err := os.ReadFile("hub-schema.json")
	require.NoError(t, err)
	assert.True(t, string(committed) == string(generated), "lib/hub/hub-schema.json is stale: run make gen, then bun run gen:api in chat/")
}
