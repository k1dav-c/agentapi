// Package selfupdatetest serves a fake latest release of AgentAPI for
// tests, and writes fake AgentAPI programs that print their version.
package selfupdatetest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/agentapi/lib/selfupdate"
)

// Release is a fake GitHub that serves one release as the latest: its
// binary for this platform and its checksums.txt.
type Release struct {
	// URL is the fake GitHub API's.
	URL string

	tag       string
	binary    []byte
	tampered  atomic.Bool
	failing   atomic.Bool
	lookups   atomic.Int32
	downloads atomic.Int32
}

// NewRelease serves version as the latest release, with binary as its
// file for this platform, until the test ends.
func NewRelease(t testing.TB, version string, binary []byte) *Release {
	t.Helper()
	r := &Release{tag: "v" + strings.TrimPrefix(version, "v"), binary: binary}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	r.URL = srv.URL
	return r
}

// Source looks the release up here.
func (r *Release) Source() selfupdate.Source {
	return selfupdate.Source{BaseURL: r.URL}
}

// Tamper has checksums.txt give another checksum for the binary.
func (r *Release) Tamper() { r.tampered.Store(true) }

// Fail has the lookup of the latest release fail.
func (r *Release) Fail() { r.failing.Store(true) }

// Lookups counts the lookups of the latest release.
func (r *Release) Lookups() int { return int(r.lookups.Load()) }

// Downloads counts the downloads of the binary.
func (r *Release) Downloads() int { return int(r.downloads.Load()) }

func (r *Release) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	asset := selfupdate.PlatformAsset()
	switch req.URL.Path {
	case fmt.Sprintf("/repos/%s/%s/releases/latest", selfupdate.Owner, selfupdate.Repo):
		r.lookups.Add(1)
		if r.failing.Load() {
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
			return
		}
		base := "http://" + req.Host
		_ = json.NewEncoder(w).Encode(selfupdate.Release{Tag: r.tag, Assets: []selfupdate.Asset{
			{Name: asset, URL: base + "/download/" + asset},
			{Name: selfupdate.ChecksumAsset, URL: base + "/download/" + selfupdate.ChecksumAsset},
		}})
	case "/download/" + asset:
		r.downloads.Add(1)
		_, _ = w.Write(r.binary)
	case "/download/" + selfupdate.ChecksumAsset:
		sum := sha256.Sum256(r.binary)
		checksum := hex.EncodeToString(sum[:])
		if r.tampered.Load() {
			checksum = strings.Repeat("0", len(checksum))
		}
		_, _ = fmt.Fprintf(w, "%s  %s\n%s  agentapi-plan9-amd64\n", checksum, asset, strings.Repeat("1", len(checksum)))
	default:
		http.NotFound(w, req)
	}
}

// Script is a fake AgentAPI program: a shell script that prints version
// as agentapi --version does.
func Script(version string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\necho \"agentapi version %s\"\n", version))
}

// WriteProgram writes Script(version) at path, creating its directory.
func WriteProgram(t testing.TB, path, version string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, Script(version), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
