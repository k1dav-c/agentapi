package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Bounds of a search for a file by path: the agent's working directory can
// be a home directory.
const (
	findMaxEntries = 50000
	findMaxTime    = 2 * time.Second
	findMaxMatches = 10
)

// findSkippedDirs are directories a search doesn't enter: dependencies and
// build caches, where a match would only mislead.
var findSkippedDirs = map[string]bool{
	"node_modules": true, "vendor": true, "__pycache__": true, "site-packages": true, "target": true, "dist": true, "build": true,
}

type FileFindResponse struct {
	Body struct {
		Matches []FileMatch `json:"matches" nullable:"false" doc:"Files whose path ends with the one asked for, the likeliest first."`
		// Complete is false when the search stopped at its bounds.
		Complete bool `json:"complete" doc:"The whole working directory was searched; false when the search stopped at its bounds (50,000 entries or 2 seconds)."`
	}
}

type FileMatch struct {
	Relative string    `json:"relative" doc:"The file's path relative to the agent's working directory."`
	Modified time.Time `json:"modified" doc:"When the file last changed."`
	// ExactCase is false for a match that differs in case only.
	ExactCase bool `json:"exact_case" doc:"The path matches with the same case."`
}

// findFile handles GET /files/find: the files under the agent's working
// directory whose path ends with the one given. The chat asks when a path
// the agent wrote isn't there: an agent working in a repository below the
// working directory names files relative to the repository, and sometimes
// in the wrong case.
func (s *Server) findFile(ctx context.Context, input *FilePathQuery) (*FileFindResponse, error) {
	if strings.TrimSpace(input.Path) == "" {
		return nil, huma.Error400BadRequest("path is empty")
	}
	if s.cwd == "" {
		return nil, huma.Error404NotFound("the agent's working directory is unknown")
	}
	want := filepath.ToSlash(filepath.Clean(input.Path))
	if filepath.IsAbs(input.Path) {
		// An absolute path outside the working directory: look for its
		// tail, as far as the last two parts go.
		parts := strings.Split(strings.TrimPrefix(want, "/"), "/")
		want = strings.Join(parts[max(0, len(parts)-2):], "/")
	}
	if want == "." || want == ".." || strings.HasPrefix(want, "../") {
		return nil, huma.Error400BadRequest("not a file path")
	}
	matches, complete := findBySuffix(ctx, s.cwd, want)
	resp := &FileFindResponse{}
	resp.Body.Matches = matches
	resp.Body.Complete = complete
	return resp, nil
}

// findBySuffix searches root for the files whose path relative to it ends
// with want, ignoring case. It goes breadth first, so the repositories
// near the top are searched before deep caches use up its bounds. Hidden
// directories and dependencies are skipped unless want goes into them. It
// reports whether the search finished within its bounds.
func findBySuffix(ctx context.Context, root, want string) ([]FileMatch, bool) {
	wantLower := strings.ToLower(want)
	deadline := time.Now().Add(findMaxTime)
	entries := 0
	var matches []FileMatch
	queue := []string{""}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		list, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue
		}
		for _, d := range list {
			entries++
			if entries > findMaxEntries || ctx.Err() != nil || (entries%1000 == 0 && time.Now().After(deadline)) {
				return sortMatches(matches), false
			}
			rel := d.Name()
			if dir != "" {
				rel = dir + "/" + d.Name()
			}
			if d.IsDir() {
				if !skipDir(rel, d.Name(), wantLower) {
					queue = append(queue, rel)
				}
				continue
			}
			if !d.Type().IsRegular() {
				continue
			}
			relLower := strings.ToLower(rel)
			if relLower != wantLower && !strings.HasSuffix(relLower, "/"+wantLower) {
				continue
			}
			info, err := d.Info()
			if err != nil {
				continue
			}
			matches = append(matches, FileMatch{
				Relative:  rel,
				Modified:  info.ModTime().UTC(),
				ExactCase: rel == want || strings.HasSuffix(rel, "/"+want),
			})
		}
	}
	return sortMatches(matches), true
}

// skipDir reports whether a search leaves out the directory rel (named
// name): hidden ones, dependencies and Go's module cache, unless the path
// searched for goes into one of them.
func skipDir(rel, name, wantLower string) bool {
	skipped := strings.HasPrefix(name, ".") || findSkippedDirs[name] || strings.HasSuffix(rel, "pkg/mod")
	return skipped && !strings.Contains("/"+wantLower+"/", "/"+strings.ToLower(name)+"/")
}

// sortMatches puts the matches with the same case first, then the latest
// changed, and keeps findMaxMatches of them.
func sortMatches(matches []FileMatch) []FileMatch {
	// The same case first, then the latest change.
	slices.SortStableFunc(matches, func(a, b FileMatch) int {
		if a.ExactCase != b.ExactCase {
			if a.ExactCase {
				return -1
			}
			return 1
		}
		return b.Modified.Compare(a.Modified)
	})
	if len(matches) > findMaxMatches {
		matches = matches[:findMaxMatches]
	}
	if matches == nil {
		matches = []FileMatch{}
	}
	return matches
}
