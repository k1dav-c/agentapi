package docscheck_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/internal/docscheck"
)

const root = "../.."

// skipped are the directories whose Markdown isn't the repository's own.
var skipped = []string{".git", "node_modules", "chat/out", "chat/.next", "lib/httpmw/chat", "out"}

// markdownFiles lists the repository's Markdown files: the tracked ones, or every one outside the
// skipped directories in a copy without git.
func markdownFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.md").Output()
	if err == nil {
		for _, f := range strings.Split(string(out), "\x00") {
			if f != "" && !isSkipped(f) {
				if _, err := os.Stat(filepath.Join(root, f)); err == nil {
					files = append(files, f)
				}
			}
		}
		return files
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() && rel != "." && isSkipped(rel) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(rel, ".md") {
			files = append(files, rel)
		}
		return nil
	})
	require.NoError(t, err)
	return files
}

func isSkipped(rel string) bool {
	for _, s := range skipped {
		if rel == s || strings.HasPrefix(rel, s+"/") || strings.Contains(rel, "/"+s+"/") {
			return true
		}
	}
	return false
}

type docs struct {
	t      *testing.T
	parsed map[string]*docscheck.Doc
}

// get parses a Markdown file once; nil if it doesn't exist.
func (d *docs) get(name string) *docscheck.Doc {
	if doc, ok := d.parsed[name]; ok {
		return doc
	}
	doc, err := docscheck.ReadFile(root, name)
	if err != nil && !os.IsNotExist(err) {
		d.t.Fatalf("read %s: %v", name, err)
	}
	d.parsed[name] = doc
	return doc
}

// TestLinks checks that every relative link and image in the docs points at a file that exists,
// and every #anchor at a heading (or <a id>) of its page, by GitHub's slugs.
func TestLinks(t *testing.T) {
	t.Parallel()
	d := &docs{t: t, parsed: map[string]*docscheck.Doc{}}
	files := markdownFiles(t)
	require.NotEmpty(t, files)
	for _, name := range files {
		for _, ref := range d.get(name).Refs {
			if !docscheck.Relative(ref.Target) {
				continue
			}
			file, anchor := docscheck.Resolve(name, ref.Target)
			if file == ".." || strings.HasPrefix(file, "../") {
				t.Errorf("%s:%d: %s points outside the repository", name, ref.Line, ref.Target)
				continue
			}
			if _, err := os.Stat(filepath.Join(root, file)); err != nil {
				t.Errorf("%s:%d: %s: %s doesn't exist", name, ref.Line, ref.Target, file)
				continue
			}
			if anchor == "" || !strings.HasSuffix(file, ".md") {
				continue
			}
			if target := d.get(file); target != nil && !target.Anchors[anchor] {
				t.Errorf("%s:%d: %s: %s has no heading with anchor #%s", name, ref.Line, ref.Target, file, anchor)
			}
		}
	}
}

// TestTwins checks that each page with a zh-TW twin (X.md and X.zh-TW.md) has the same structure
// as it: headings of the same levels in the same order, as many fenced code blocks, the same
// images in the same order, and links to the same files.
func TestTwins(t *testing.T) {
	t.Parallel()
	files := markdownFiles(t)
	var pairs int
	for _, name := range files {
		if !strings.HasSuffix(name, ".zh-TW.md") {
			continue
		}
		en := strings.TrimSuffix(name, ".zh-TW.md") + ".md"
		if !slices.Contains(files, en) {
			continue
		}
		pairs++
		a, err := docscheck.ReadFile(root, en)
		require.NoError(t, err)
		b, err := docscheck.ReadFile(root, name)
		require.NoError(t, err)
		for _, problem := range docscheck.CompareTwins(a, b) {
			t.Error(problem)
		}
	}
	assert.Positive(t, pairs, "no zh-TW twins found")
}

func TestSlug(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"Quick start":                     "quick-start",
		"`--state-db` and `/events`":      "--state-db-and-events",
		"Claude Code, Codex & Pi":         "claude-code-codex--pi",
		"See [the API](docs/api.md) here": "see-the-api-here",
		"<a id=\"x\"></a>Resource usage":  "resource-usage",
		"請求與決策卡片":                         "請求與決策卡片",
		"提醒、連線復原與快取":                      "提醒連線復原與快取",
		"snake_case (v1.2)":               "snake_case-v12",
		"Ünïcode — dash":                  "ünïcode--dash",
	} {
		assert.Equal(t, want, docscheck.Slug(text), text)
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	doc := docscheck.Parse("docs/x.md", strings.Join([]string{
		"# Title",                              // 1
		"## Usage",                             // 2
		"## Usage",                             // 3
		"```bash",                              // 4
		"# not a heading [a](nope.md)",         // 5
		"```",                                  // 6
		"   ~~~",                               // 7
		"   ```",                               // 8
		"   ~~~",                               // 9
		"See [a](a.md#usage) and `[b](b.md)`.", // 10
		"[![badge](img/b.svg)](c.md) <a id=\"k\"></a>",            // 11
		"<img alt=\"x\" src=\"shot.png\"> <a href=\"d.md\">d</a>", // 12
		"[ref]: e.md", // 13
	}, "\n"))
	var levels []int
	for _, h := range doc.Headings {
		levels = append(levels, h.Level)
	}
	assert.Equal(t, []int{1, 2, 2}, levels)
	assert.Equal(t, "usage-1", doc.Headings[2].Slug)
	assert.Equal(t, []int{4, 7}, doc.Fences)
	assert.True(t, doc.Anchors["k"])
	var refs []string
	for _, r := range doc.Refs {
		refs = append(refs, fmt.Sprintf("%d %s %v", r.Line, r.Target, r.Image))
	}
	assert.Equal(t, []string{
		"10 a.md#usage false",
		"11 img/b.svg true",
		"11 c.md false",
		"12 shot.png true",
		"12 d.md false",
		"13 e.md false",
	}, refs)
	file, anchor := docscheck.Resolve("docs/x.md", "../README.zh-TW.md#%E5%BF%AB")
	assert.Equal(t, "README.zh-TW.md", file)
	assert.Equal(t, "快", anchor)
	file, anchor = docscheck.Resolve("docs/x.md", "#usage")
	assert.Equal(t, "docs/x.md", file)
	assert.Equal(t, "usage", anchor)
}

func TestCompareTwins(t *testing.T) {
	t.Parallel()
	en := docscheck.Parse("x.md", "# A\n## B\n![s](s.png)\n[c](c.md#one)\n```\nx\n```\n")
	zh := docscheck.Parse("x.zh-TW.md", "# 甲\n## 乙\n![s](s.png)\n[丙](c.zh-TW.md#一)\n```\nx\n```\n")
	assert.Empty(t, docscheck.CompareTwins(en, zh))

	zh = docscheck.Parse("x.zh-TW.md", "# 甲\n### 乙\n![s](t.png)\n[丁](d.zh-TW.md)\n")
	problems := docscheck.CompareTwins(en, zh)
	require.Len(t, problems, 5, strings.Join(problems, "\n"))
	assert.Contains(t, problems[0], "x.md:2")
	assert.Contains(t, problems[0], "x.zh-TW.md:2")
}
