package httpapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A home directory with repositories below it: the agent names their
	// files relative to the repository, sometimes in the wrong case.
	writeFile(t, filepath.Join(dir, "law-mcp", "docs", "design.md"), "# Design\n")
	writeFile(t, filepath.Join(dir, "app", "docs", "DESIGN.md"), "# Design\n")
	writeFile(t, filepath.Join(dir, "app", "node_modules", "pkg", "docs", "DESIGN.md"), "dependency")
	writeFile(t, filepath.Join(dir, ".cache", "docs", "DESIGN.md"), "cache")
	writeFile(t, filepath.Join(dir, "go", "pkg", "mod", "x", "docs", "DESIGN.md"), "module cache")
	writeFile(t, filepath.Join(dir, "app", ".github", "workflows", "ci.yml"), "on: push\n")
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "law-mcp", "docs", "design.md"), old, old))
	s := &Server{cwd: dir}
	find := func(path string) []FileMatch {
		t.Helper()
		resp, err := s.findFile(t.Context(), &FilePathQuery{Path: path})
		require.NoError(t, err)
		assert.True(t, resp.Body.Complete)
		return resp.Body.Matches
	}

	matches := find("docs/DESIGN.md")
	require.Len(t, matches, 2, "not in dependencies, hidden directories or the module cache")
	assert.Equal(t, "app/docs/DESIGN.md", matches[0].Relative, "the same case first")
	assert.True(t, matches[0].ExactCase)
	assert.Equal(t, "law-mcp/docs/design.md", matches[1].Relative)
	assert.False(t, matches[1].ExactCase)

	// A path into a hidden directory searches it.
	matches = find(".github/workflows/ci.yml")
	require.Len(t, matches, 1)
	assert.Equal(t, "app/.github/workflows/ci.yml", matches[0].Relative)

	// The tail of an absolute path from elsewhere.
	matches = find("/somewhere/else/docs/design.md")
	assert.Len(t, matches, 2)

	assert.Empty(t, find("docs/missing.md"))
	_, err := s.findFile(t.Context(), &FilePathQuery{Path: "../x"})
	assert.Equal(t, 400, statusOf(t, err))
}
