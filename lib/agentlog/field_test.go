package agentlog

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestField(t *testing.T) {
	t.Parallel()
	line := []byte(`{"parentUuid":null,"message":{"role":"user","content":[{"type":"tool_result","content":"{\"timestamp\":\"2019-01-01T00:00:00Z\",\"type\":\"x\"} \\\" }"}]},"n":-1.5e3,"ok":true,"type":"user","timestamp":"2026-10-20T10:00:00.000Z"}` + "\n")
	for key, want := range map[string]string{
		"type":       `"user"`,
		"timestamp":  `"2026-10-20T10:00:00.000Z"`,
		"n":          `-1.5e3`,
		"ok":         `true`,
		"parentUuid": `null`,
	} {
		value, ok := Field(line, key)
		assert.True(t, ok, key)
		assert.Equal(t, want, string(value), key)
	}
	_, ok := Field(line, "role")
	assert.False(t, ok, "only top-level keys")

	typ, ok := StringField(line, "type")
	assert.True(t, ok)
	assert.Equal(t, "user", typ)
	at, ok := LineTime(line)
	assert.True(t, ok)
	assert.Equal(t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), at, "not the one in the tool result")

	for _, bad := range []string{``, `[]`, `"type"`, `{"type":"us`, `{"a":{"b":1}`, `{"a" 1,"type":"x"}`, `{"a":1 "type":"x"}`} {
		_, ok := Field([]byte(bad), "type")
		assert.False(t, ok, bad)
	}
	value, ok := Field([]byte(` { "type" : "x" } `), "type")
	assert.True(t, ok)
	assert.Equal(t, `"x"`, string(value))

	cwd, ok := StringField([]byte(`{"cwd":"C:\\Users\\me \u65e5"}`), "cwd")
	assert.True(t, ok)
	assert.Equal(t, `C:\Users\me 日`, cwd, "escapes are decoded")

	long := `{"message":"` + strings.Repeat(`a\"b`, 100000) + `","type":"assistant"}`
	typ, ok = StringField([]byte(long), "type")
	assert.True(t, ok)
	assert.Equal(t, "assistant", typ)
}

func TestElements(t *testing.T) {
	t.Parallel()
	var types []string
	ok := Elements([]byte(`[{"type":"tool_result","content":"a ] } \"b"}, {"type":"text","text":"hi"} ,"s",1]`), func(elem []byte) bool {
		typ, _ := StringField(elem, "type")
		types = append(types, typ)
		return true
	})
	assert.True(t, ok)
	assert.Equal(t, []string{"tool_result", "text", "", ""}, types)

	n := 0
	assert.True(t, Elements([]byte(`[1,2,3]`), func([]byte) bool { n++; return n < 2 }))
	assert.Equal(t, 2, n, "until fn returns false")
	assert.True(t, Elements([]byte(` [ ] `), func([]byte) bool { t.Fatal("no elements"); return false }))
	for _, bad := range []string{`{}`, `"[]"`, `[1,2`, `[1 2]`, ``} {
		assert.False(t, Elements([]byte(bad), func([]byte) bool { return true }), bad)
	}
}
