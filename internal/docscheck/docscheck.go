// Package docscheck reads the repository's Markdown the way GitHub renders it, for the tests that
// check the docs: every relative link, image and anchor resolves, and each English page and its
// zh-TW twin have the same structure.
package docscheck

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Doc is what the checks read of one Markdown file.
type Doc struct {
	Path     string    // slash-separated, relative to the repository root
	Headings []Heading // in order, outside code blocks
	Fences   []int     // the line of each fenced code block's opening fence
	Refs     []Ref     // links and images, in order, outside code blocks and code spans
	Anchors  map[string]bool
}

// Heading is an ATX heading.
type Heading struct {
	Line  int
	Level int
	Text  string
	Slug  string // with the -1, -2… GitHub adds to repeated slugs
}

// Ref is a link or image target.
type Ref struct {
	Line   int
	Target string // as written
	Image  bool
}

var (
	headingRe  = regexp.MustCompile(`^(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	linkTextRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	tagRe      = regexp.MustCompile(`<[^>]+>`)
	anchorRe   = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`)
	// The end of an inline link or image, with its target (optionally <bracketed>); a reference
	// definition; or an HTML href/src attribute.
	linkRe   = regexp.MustCompile(`\]\(\s*(?:<([^>]*)>|([^)\s]+))|^ {0,3}\[[^\]]+\]:\s+<?([^>\s]+)|(?:(<img\b[^>]*?\s)|\s)(?:href|src)="([^"]+)"`)
	schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// Slug is GitHub's anchor for a heading's text: link syntax and HTML tags stripped, lowercase,
// only Unicode letters, marks, numbers, connector punctuation, spaces and '-' kept, and spaces
// turned into '-'.
func Slug(text string) string {
	text = linkTextRe.ReplaceAllString(text, "$1")
	text = strings.ToLower(strings.TrimSpace(tagRe.ReplaceAllString(text, "")))
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.Pc):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Parse reads a Markdown file's headings, fenced code blocks, links, images and anchors.
func Parse(name, text string) *Doc {
	d := &Doc{Path: name, Anchors: map[string]bool{}}
	seen := map[string]int{}
	fence := "" // the open fence's marker, e.g. "```"
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		trimmed := strings.TrimLeft(line, " \t")
		if marker := fenceMarker(trimmed); marker != "" {
			switch {
			case fence == "":
				fence = marker
				d.Fences = append(d.Fences, n)
				continue
			case marker[0] == fence[0] && len(marker) >= len(fence) && strings.TrimSpace(trimmed[len(marker):]) == "":
				fence = ""
				continue
			}
		}
		if fence != "" {
			continue
		}
		if m := headingRe.FindStringSubmatch(line); m != nil {
			s := Slug(m[2])
			k := seen[s]
			seen[s] = k + 1
			if k > 0 {
				s = fmt.Sprintf("%s-%d", s, k)
			}
			d.Anchors[s] = true
			d.Headings = append(d.Headings, Heading{Line: n, Level: len(m[1]), Text: m[2], Slug: s})
		}
		for _, m := range anchorRe.FindAllStringSubmatch(line, -1) {
			d.Anchors[m[1]] = true
		}
		// Code spans render as text, so a link in one isn't a link.
		plain := blankCodeSpans(line)
		for _, m := range linkRe.FindAllStringSubmatchIndex(plain, -1) {
			group := func(k int) string {
				if m[2*k] < 0 {
					return ""
				}
				return plain[m[2*k]:m[2*k+1]]
			}
			switch {
			case m[2] >= 0 || m[4] >= 0:
				d.Refs = append(d.Refs, Ref{Line: n, Target: group(1) + group(2), Image: isImage(plain, m[0])})
			case m[6] >= 0:
				d.Refs = append(d.Refs, Ref{Line: n, Target: group(3)})
			case m[10] >= 0:
				d.Refs = append(d.Refs, Ref{Line: n, Target: group(5), Image: m[8] >= 0})
			}
		}
	}
	return d
}

// blankCodeSpans replaces each code span, a run of backticks up to the next run of the same
// length, with spaces.
func blankCodeSpans(line string) string {
	b := []byte(line)
	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++
			continue
		}
		n := run(b, i)
		end := -1
		for j := i + n; j < len(b); {
			if b[j] != '`' {
				j++
				continue
			}
			if k := run(b, j); k == n {
				end = j + k
				break
			} else {
				j += k
			}
		}
		if end < 0 {
			i += n
			continue
		}
		for k := i; k < end; k++ {
			b[k] = ' '
		}
		i = end
	}
	return string(b)
}

func run(b []byte, i int) int {
	n := 0
	for i+n < len(b) && b[i+n] == b[i] {
		n++
	}
	return n
}

// isImage reports whether the "](" at end closes an image: its matching '[' follows a '!'.
func isImage(line string, end int) bool {
	depth := 0
	for i := end; i >= 0; i-- {
		switch line[i] {
		case ']':
			depth++
		case '[':
			depth--
			if depth == 0 {
				return i > 0 && line[i-1] == '!'
			}
		}
	}
	return false
}

// fenceMarker is the run of three or more backticks or tildes a line starts with, or "".
func fenceMarker(line string) string {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	n := 0
	for n < len(line) && line[n] == line[0] {
		n++
	}
	if n < 3 || (line[0] == '`' && strings.Contains(line[n:], "`")) {
		return ""
	}
	return line[:n]
}

// Relative reports whether a target points into the repository: not a URL with a scheme, and not
// a site-absolute path.
func Relative(target string) bool {
	return !schemeRe.MatchString(target) && !strings.HasPrefix(target, "/")
}

// Resolve splits a relative target into the repository path it points at (the file itself for a
// bare "#anchor") and its unescaped anchor.
func Resolve(from, target string) (file, anchor string) {
	ref, anchor, _ := strings.Cut(target, "#")
	if a, err := url.PathUnescape(anchor); err == nil {
		anchor = a
	}
	if ref == "" {
		return from, anchor
	}
	if i := strings.IndexByte(ref, '?'); i >= 0 {
		ref = ref[:i]
	}
	if r, err := url.PathUnescape(ref); err == nil {
		ref = r
	}
	return path.Join(path.Dir(from), ref), anchor
}

// ReadFile parses a file of the repository at root.
func ReadFile(root, name string) (*Doc, error) {
	b, err := os.ReadFile(path.Join(root, name))
	if err != nil {
		return nil, err
	}
	return Parse(name, string(b)), nil
}

// CompareTwins lists how a page's zh-TW twin differs in structure from the English page: heading
// levels in order, fenced code blocks (section by section when the headings match), images in
// order, and the files linked to, with .zh-TW and anchors dropped.
func CompareTwins(en, zh *Doc) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	headingsMatch := len(en.Headings) == len(zh.Headings)
	for i := 0; i < max(len(en.Headings), len(zh.Headings)); i++ {
		switch {
		case i >= len(en.Headings):
			h := zh.Headings[i]
			add("%s:%d: heading %q has no counterpart in %s (it has %d headings, the twin %d)", zh.Path, h.Line, h.Text, en.Path, len(en.Headings), len(zh.Headings))
		case i >= len(zh.Headings):
			h := en.Headings[i]
			add("%s:%d: heading %q has no counterpart in %s (it has %d headings, the twin %d)", en.Path, h.Line, h.Text, zh.Path, len(en.Headings), len(zh.Headings))
		case en.Headings[i].Level != zh.Headings[i].Level:
			a, b := en.Headings[i], zh.Headings[i]
			add("%s:%d and %s:%d: heading %d is level %d (%q) in one and level %d (%q) in the other", en.Path, a.Line, zh.Path, b.Line, i+1, a.Level, a.Text, b.Level, b.Text)
		default:
			continue
		}
		headingsMatch = false
		break
	}

	if headingsMatch {
		a, b := fencesBySection(en), fencesBySection(zh)
		for i := range a {
			if len(a[i]) != len(b[i]) {
				add("%s and %s: section %s has %d fenced code blocks in one (lines %v) and %d in the other (lines %v)",
					en.Path, zh.Path, sectionName(en, zh, i), len(a[i]), a[i], len(b[i]), b[i])
			}
		}
	} else if len(en.Fences) != len(zh.Fences) {
		add("%s has %d fenced code blocks, %s %d", en.Path, len(en.Fences), zh.Path, len(zh.Fences))
	}

	ia, ib := images(en), images(zh)
	for i := 0; i < max(len(ia), len(ib)); i++ {
		switch {
		case i >= len(ia):
			add("%s:%d: image %s has no counterpart in %s", zh.Path, ib[i].Line, ib[i].Target, en.Path)
		case i >= len(ib):
			add("%s:%d: image %s has no counterpart in %s", en.Path, ia[i].Line, ia[i].Target, zh.Path)
		case ia[i].Target != ib[i].Target:
			add("%s:%d and %s:%d: image %d is %s in one and %s in the other", en.Path, ia[i].Line, zh.Path, ib[i].Line, i+1, ia[i].Target, ib[i].Target)
		default:
			continue
		}
		break
	}

	la, lb := linkedFiles(en), linkedFiles(zh)
	for _, f := range sortedKeys(la) {
		if _, ok := lb[f]; !ok {
			add("%s:%d: links to %s, which %s doesn't", en.Path, la[f].Line, la[f].Target, zh.Path)
		}
	}
	for _, f := range sortedKeys(lb) {
		if _, ok := la[f]; !ok {
			add("%s:%d: links to %s, which %s doesn't", zh.Path, lb[f].Line, lb[f].Target, en.Path)
		}
	}
	return problems
}

// fencesBySection groups the opening lines of the fenced code blocks by the heading before them
// (index 0: before the first heading).
func fencesBySection(d *Doc) [][]int {
	out := make([][]int, len(d.Headings)+1)
	h := 0
	for _, f := range d.Fences {
		for h < len(d.Headings) && d.Headings[h].Line < f {
			h++
		}
		out[h] = append(out[h], f)
	}
	return out
}

func sectionName(en, zh *Doc, i int) string {
	if i == 0 {
		return "before the first heading"
	}
	a, b := en.Headings[i-1], zh.Headings[i-1]
	return fmt.Sprintf("%q (%s:%d) / %q (%s:%d)", a.Text, en.Path, a.Line, b.Text, zh.Path, b.Line)
}

func images(d *Doc) []Ref {
	var out []Ref
	for _, r := range d.Refs {
		if r.Image {
			out = append(out, r)
		}
	}
	return out
}

// linkedFiles maps each file a page links to (other than itself), with .zh-TW dropped, to the
// first link to it.
func linkedFiles(d *Doc) map[string]Ref {
	out := map[string]Ref{}
	self := strings.Replace(d.Path, ".zh-TW.md", ".md", 1)
	for _, r := range d.Refs {
		if r.Image || !Relative(r.Target) {
			continue
		}
		file, _ := Resolve(d.Path, r.Target)
		file = strings.Replace(file, ".zh-TW.md", ".md", 1)
		if _, ok := out[file]; !ok && file != self {
			out[file] = r
		}
	}
	return out
}

func sortedKeys(m map[string]Ref) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
