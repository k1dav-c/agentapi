package native_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/native"
)

func TestOneLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ls -la", native.OneLine("  ls -la  ", 120))
	assert.Equal(t, "node <<'JS' …", native.OneLine("node <<'JS'\nconsole.log(1)\nJS", 120))
	long := native.OneLine("echo "+strings.Repeat("中文", 100), 120)
	assert.Equal(t, 120, utf8.RuneCountInString(long))
	assert.True(t, strings.HasSuffix(long, "…"))
	assert.True(t, utf8.ValidString(long))
	assert.Empty(t, native.OneLine("  \n ", 120))
}
