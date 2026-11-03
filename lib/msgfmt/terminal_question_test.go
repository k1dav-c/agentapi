package msgfmt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScreenTail(t *testing.T) {
	naive := func(screen string, n int) string {
		lines := strings.Split(strings.TrimRight(screen, " \t\r\n"), "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		return strings.Join(lines, "\n")
	}
	for _, screen := range []string{"", "one", "a\nb\nc", "a\nb\nc\n\n  \n", "\n\na\n", strings.Repeat("x\n", 100) + "end  \n \n"} {
		for _, n := range []int{1, 2, 3, 40, 200} {
			require.Equal(t, naive(screen, n), ScreenTail(screen, n), "screen %q n %d", screen, n)
		}
	}
}
