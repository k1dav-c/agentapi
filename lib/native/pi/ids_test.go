package pi

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Messages of one millisecond get IDs of their own; the IDs remembered for
// that stay bounded however long the conversation.
func TestUniqueIDIsBounded(t *testing.T) {
	t.Parallel()
	l := loopState{usedIDs: map[string]bool{}}
	assert.Equal(t, "assistant-1", l.uniqueID("assistant-1"))
	assert.Equal(t, "assistant-1-2", l.uniqueID("assistant-1"))
	assert.Equal(t, "assistant-1-3", l.uniqueID("assistant-1"))
	for i := range 10 * maxUsedIDs {
		l.uniqueID(fmt.Sprintf("user-%d", i+2))
	}
	assert.Len(t, l.usedIDs, maxUsedIDs)
	assert.Len(t, l.usedOrder, maxUsedIDs)
	assert.Equal(t, "user-11", l.uniqueID("user-11"), "a long-gone ID is forgotten")
	last := fmt.Sprintf("user-%d", 10*maxUsedIDs+1)
	assert.Equal(t, last+"-2", l.uniqueID(last))
}
