package screentracker

import (
	"embed"
	"path"
	"testing"

	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/stretchr/testify/assert"
)

//go:embed testdata
var testdataDir embed.FS

func TestScreenDiff(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		assert.Equal(t, "", screenDiff("123456", "123456", msgfmt.AgentTypeCustom))
		assert.Equal(t, "1234567", screenDiff("123456", "1234567", msgfmt.AgentTypeCustom))
		assert.Equal(t, "42", screenDiff("123", "123\n  \n \n \n42", msgfmt.AgentTypeCustom))
		assert.Equal(t, "12342", screenDiff("123", "12342\n   \n \n \n", msgfmt.AgentTypeCustom))
		assert.Equal(t, "42", screenDiff("123", "123\n  \n \n \n42\n   \n \n \n", msgfmt.AgentTypeCustom))
		assert.Equal(t, "42", screenDiff("89", "42", msgfmt.AgentTypeCustom))
	})

	dir := "testdata/diff"
	cases, err := testdataDir.ReadDir(dir)
	assert.NoError(t, err)
	for _, c := range cases {
		t.Run(c.Name(), func(t *testing.T) {
			before, err := testdataDir.ReadFile(path.Join(dir, c.Name(), "before.txt"))
			assert.NoError(t, err)
			after, err := testdataDir.ReadFile(path.Join(dir, c.Name(), "after.txt"))
			assert.NoError(t, err)
			expected, err := testdataDir.ReadFile(path.Join(dir, c.Name(), "expected.txt"))
			assert.NoError(t, err)
			assert.Equal(t, string(expected), screenDiff(string(before), string(after), msgfmt.AgentTypeCustom))
		})
	}
}

func TestTrimPreviousMessageOverlap(t *testing.T) {
	t.Run("no overlap", func(t *testing.T) {
		assert.Equal(t, "new content", trimPreviousMessageOverlap("previous message", "new content"))
	})
	t.Run("empty inputs", func(t *testing.T) {
		assert.Equal(t, "new", trimPreviousMessageOverlap("", "new"))
		assert.Equal(t, "", trimPreviousMessageOverlap("prev", ""))
	})
	t.Run("full previous tail leaks into new head", func(t *testing.T) {
		prev := "• first answer line\n  second answer line"
		newMsg := "  second answer line\n\n› next question\n\n• new answer"
		assert.Equal(t, "› next question\n\n• new answer", trimPreviousMessageOverlap(prev, newMsg))
	})
	t.Run("multi line overlap", func(t *testing.T) {
		prev := "a\nb\nc\nd"
		newMsg := "c\nd\nnew line"
		assert.Equal(t, "new line", trimPreviousMessageOverlap(prev, newMsg))
	})
	t.Run("trailing whitespace is ignored in comparison", func(t *testing.T) {
		prev := "line one                    \nline two                    "
		newMsg := "line two\nfresh"
		assert.Equal(t, "fresh", trimPreviousMessageOverlap(prev, newMsg))
	})
	t.Run("whitespace only overlap is not trimmed", func(t *testing.T) {
		prev := "content\n   "
		newMsg := "   \ndifferent"
		assert.Equal(t, "   \ndifferent", trimPreviousMessageOverlap(prev, newMsg))
	})
	t.Run("entire new message is an overlap", func(t *testing.T) {
		prev := "a\nb\nc"
		newMsg := "b\nc"
		assert.Equal(t, "", trimPreviousMessageOverlap(prev, newMsg))
	})
	t.Run("largest overlap wins", func(t *testing.T) {
		// prev tail "x\ny\nx\ny" vs new head: use the longest match
		prev := "x\ny\nx\ny"
		newMsg := "x\ny\nx\ny\nnew"
		assert.Equal(t, "new", trimPreviousMessageOverlap(prev, newMsg))
	})
	t.Run("partial line match does not count", func(t *testing.T) {
		prev := "the quick brown fox"
		newMsg := "the quick brown\nfox jumps"
		assert.Equal(t, newMsg, trimPreviousMessageOverlap(prev, newMsg))
	})
}
