// Package host implements agentapi host: the launcher on a machine that
// starts an AgentAPI for each GitHub issue a hub dispatches to it, in a git
// worktree of its own, and stops it again. See
// docs/specs/issue-dispatch.zh-TW.md.
package host

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Key names a job: a GitHub issue, owner/repo#issue.
type Key struct {
	Owner string
	Name  string
	Issue int
}

// nameRE is what GitHub allows in an owner or repository name.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ParseKey reads a key as String or Slug writes it.
func ParseKey(s string) (Key, error) {
	var repo, issue string
	if before, after, ok := strings.Cut(s, "#"); ok {
		repo, issue = before, after
	} else if parts := strings.Split(s, "~"); len(parts) == 3 {
		repo, issue = parts[0]+"/"+parts[1], parts[2]
	} else {
		return Key{}, fmt.Errorf("job %q: want owner/repo#issue", s)
	}
	owner, name, err := ParseRepo(repo)
	if err != nil {
		return Key{}, fmt.Errorf("job %q: %w", s, err)
	}
	n, err := strconv.Atoi(issue)
	if err != nil || n <= 0 || strconv.Itoa(n) != issue {
		return Key{}, fmt.Errorf("job %q: the issue is a positive number", s)
	}
	return Key{Owner: owner, Name: name, Issue: n}, nil
}

// ParseRepo splits owner/name, refusing names GitHub doesn't allow.
func ParseRepo(s string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || !validName(owner) || !validName(name) {
		return "", "", fmt.Errorf("repository %q: want owner/name", s)
	}
	return owner, name, nil
}

func validName(s string) bool {
	return nameRE.MatchString(s) && s != "." && s != ".."
}

// String is owner/repo#issue.
func (k Key) String() string { return fmt.Sprintf("%s/%s#%d", k.Owner, k.Name, k.Issue) }

// Slug is owner~repo~issue, for paths and file names.
func (k Key) Slug() string { return fmt.Sprintf("%s~%s~%d", k.Owner, k.Name, k.Issue) }

// Repo is owner/repo.
func (k Key) Repo() string { return k.Owner + "/" + k.Name }

// Branch is the job's branch.
func (k Key) Branch() string { return fmt.Sprintf("agent/issue-%d", k.Issue) }
