package transcript

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRichMessageClone(t *testing.T) {
	t.Parallel()
	isError := true
	m := RichMessage{
		MessageID: "m1",
		Role:      "assistant",
		Content: []RichContentBlock{
			{Type: "tool_use", ToolUseID: "t1", ToolInput: json.RawMessage(`{"a":1}`)},
			{Type: "tool_result", ToolUseID: "t1", IsError: &isError, Images: []RichImage{{MediaType: "image/png", Data: []byte("png")}}},
		},
		Usage: &Usage{InputTokens: 3},
	}
	c := m.Clone()
	assert.Equal(t, m, c)

	c.Content[0].ToolInput[2] = 'b'
	c.Content[1].Images[0].URL = "/uploads/x/image.png"
	*c.Content[1].IsError = false
	c.Usage.InputTokens = 4
	c.Content = append(c.Content, RichContentBlock{Type: "text"})
	assert.JSONEq(t, `{"a":1}`, string(m.Content[0].ToolInput))
	assert.Empty(t, m.Content[1].Images[0].URL)
	assert.True(t, *m.Content[1].IsError)
	assert.Equal(t, 3, m.Usage.InputTokens)
	assert.Len(t, m.Content, 2)

	assert.Equal(t, RichMessage{}, RichMessage{}.Clone(), "nothing stays nothing")
}
