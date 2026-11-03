// Package selfupdate installs the latest official release of AgentAPI
// (k1dav-c/agentapi on GitHub) in place of the program that runs: it looks
// the release up, picks the binary for the platform, checks it against the
// release's checksums.txt and replaces the program with it, under a lock
// file that the AgentAPIs sharing the program take in turn.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const (
	// Owner and Repo name the repository whose releases are installed.
	Owner = "k1dav-c"
	Repo  = "agentapi"
	// ChecksumAsset is the release's SHA-256 manifest.
	ChecksumAsset = "checksums.txt"

	maxBinarySize           = 256 << 20
	maxChecksumManifestSize = 1 << 20
	userAgent               = "agentapi-update"
)

// githubAPI is GitHub's API. Tests that build AgentAPI stamp a fake's
// with -ldflags -X; nothing changes it at run time.
var githubAPI = "https://api.github.com"

// Source is where releases are looked up and downloaded from.
type Source struct {
	// BaseURL is GitHub's API, empty for api.github.com; tests point it at
	// a fake.
	BaseURL string
}

// Asset is a file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Release is a release of AgentAPI.
type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

// Latest looks up the latest release.
func (s Source) Latest(ctx context.Context) (*Release, error) {
	base := s.BaseURL
	if base == "" {
		base = githubAPI
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(base, "/"), Owner, Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request GitHub API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release info: %w", err)
	}
	return &release, nil
}

// ParseVersion parses a version or a release tag (v1.2.3).
func ParseVersion(v string) (*semver.Version, error) {
	sv, err := semver.NewVersion(v)
	if err != nil {
		return nil, fmt.Errorf("parse version %q: %w", v, err)
	}
	return sv, nil
}

// Version is the release's version, from its tag.
func (r *Release) Version() (*semver.Version, error) {
	return ParseVersion(r.Tag)
}

// PlatformAsset is the name of the release binary for the running OS and
// architecture.
func PlatformAsset() string {
	return AssetName(runtime.GOOS, runtime.GOARCH)
}

// AssetName is the name of the release binary for an OS and architecture.
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("agentapi-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Asset returns the release's file name.
func (r *Release) Asset(name string) (Asset, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	available := make([]string, 0, len(r.Assets))
	for _, a := range r.Assets {
		available = append(available, a.Name)
	}
	return Asset{}, fmt.Errorf("no release binary %q found in release %s (available: %v)", name, r.Tag, available)
}

// Download downloads the release's file asset into a temporary file in
// dir, the directory of the program it replaces, checks it against the
// release's checksums.txt and makes it executable. It returns the file's
// path; the caller renames it into place or removes it. A release without
// a manifest, or one that doesn't match, is refused.
func (s Source) Download(ctx context.Context, r *Release, asset, dir string) (string, error) {
	binary, err := r.Asset(asset)
	if err != nil {
		return "", err
	}
	checksum, err := expectedChecksum(ctx, r, asset)
	if err != nil {
		return "", fmt.Errorf("verify release metadata: %w", err)
	}
	path, err := downloadBinary(ctx, binary.URL, dir, checksum)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	return path, nil
}

// Replace puts the file at newPath in place of the program at path, in the
// same directory: a rename, so that a process that runs the old program
// keeps its file.
func Replace(newPath, path string) error {
	return replaceBinary(newPath, path)
}

// parseChecksumManifest returns the SHA-256 checksums.txt gives for
// assetName.
func parseChecksumManifest(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		checksum := strings.ToLower(fields[0])
		decoded, err := hex.DecodeString(checksum)
		if err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("invalid SHA-256 checksum for %s", assetName)
		}
		return checksum, nil
	}
	return "", fmt.Errorf("checksum manifest does not contain %s", assetName)
}

// expectedChecksum downloads the release's checksums.txt and returns the
// checksum of assetName.
func expectedChecksum(ctx context.Context, r *Release, assetName string) (string, error) {
	manifest, err := r.Asset(ChecksumAsset)
	if err != nil {
		return "", fmt.Errorf("release has no %s: %w", ChecksumAsset, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifest.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create checksum request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download checksum manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum manifest download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumManifestSize+1))
	if err != nil {
		return "", fmt.Errorf("read checksum manifest: %w", err)
	}
	if len(data) > maxChecksumManifestSize {
		return "", fmt.Errorf("checksum manifest exceeds %d bytes", maxChecksumManifestSize)
	}
	return parseChecksumManifest(data, assetName)
}

// downloadBinary downloads url into a temporary file in destDir, refuses
// it unless its SHA-256 is expectedChecksum, and makes it executable.
func downloadBinary(ctx context.Context, url, destDir, expectedChecksum string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download binary: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp(destDir, "agentapi-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmpFile, hash), io.LimitReader(resp.Body, maxBinarySize+1))
	if err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write downloaded binary: %w", err)
	}
	if written > maxBinarySize {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("downloaded binary exceeds %d bytes", maxBinarySize)
	}
	actualChecksum := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualChecksum, expectedChecksum) {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}
	if err := tmpFile.Chmod(0o755); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("set executable permission: %w", err)
	}
	// On disk before the rename makes it the program: after a crash the
	// program is the old file or the whole new one, not an empty one.
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", err)
	}

	return tmpPath, nil
}
