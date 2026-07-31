package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAssetName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		goos   string
		goarch string
		want   string
	}{
		{"linux", "amd64", "agentapi-linux-amd64"},
		{"linux", "arm64", "agentapi-linux-arm64"},
		{"darwin", "amd64", "agentapi-darwin-amd64"},
		{"darwin", "arm64", "agentapi-darwin-arm64"},
		{"windows", "amd64", "agentapi-windows-amd64.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := buildAssetName(tt.goos, tt.goarch)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindAssetURL(t *testing.T) {
	t.Parallel()

	info := &releaseInfo{
		TagName: "v0.13.0",
		Assets: []releaseAsset{
			{Name: "agentapi-linux-amd64", BrowserDownloadURL: "https://example.com/linux-amd64"},
			{Name: "agentapi-darwin-arm64", BrowserDownloadURL: "https://example.com/darwin-arm64"},
			{Name: "agentapi-windows-amd64.exe", BrowserDownloadURL: "https://example.com/windows-amd64.exe"},
		},
	}

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		url, err := findAssetURL(info, "agentapi-linux-amd64")
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/linux-amd64", url)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		_, err := findAssetURL(info, "agentapi-freebsd-amd64")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no release binary")
		assert.Contains(t, err.Error(), "agentapi-freebsd-amd64")
	})
}

func TestParseVersion(t *testing.T) {
	t.Parallel()

	t.Run("valid without v prefix", func(t *testing.T) {
		t.Parallel()
		v, err := parseVersion("0.12.2")
		require.NoError(t, err)
		assert.Equal(t, "0.12.2", v.String())
	})

	t.Run("valid with v prefix", func(t *testing.T) {
		t.Parallel()
		v, err := parseVersion("v0.13.0")
		require.NoError(t, err)
		assert.Equal(t, "0.13.0", v.String())
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		_, err := parseVersion("dev")
		require.Error(t, err)
	})
}

func TestVersionComparison(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		current   string
		latest    string
		wantNewer bool
	}{
		{"newer available", "0.12.2", "0.13.0", true},
		{"same version", "0.12.2", "0.12.2", false},
		{"current is newer", "0.13.0", "0.12.2", false},
		{"patch update", "0.12.2", "0.12.3", true},
		{"major update", "0.12.2", "1.0.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			current, err := parseVersion(tt.current)
			require.NoError(t, err)
			latest, err := parseVersion(tt.latest)
			require.NoError(t, err)
			assert.Equal(t, tt.wantNewer, latest.GreaterThan(current))
		})
	}
}

func TestFetchLatestRelease(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		release := releaseInfo{
			TagName: "v0.13.0",
			Assets: []releaseAsset{
				{Name: "agentapi-linux-amd64", BrowserDownloadURL: "https://example.com/dl"},
			},
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/repos/k1dav-c/agentapi/releases/latest", r.URL.Path)
			assert.NotEmpty(t, r.Header.Get("User-Agent"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(release)
		}))
		defer srv.Close()

		origURL := apiBaseURL
		apiBaseURL = srv.URL
		defer func() { apiBaseURL = origURL }()

		info, err := fetchLatestRelease(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "v0.13.0", info.TagName)
		assert.Len(t, info.Assets, 1)
	})

	t.Run("non-200 response", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}))
		defer srv.Close()

		origURL := apiBaseURL
		apiBaseURL = srv.URL
		defer func() { apiBaseURL = origURL }()

		_, err := fetchLatestRelease(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})
}

func TestDownloadBinary(t *testing.T) {
	t.Parallel()

	content := []byte("#!/bin/sh\necho hello\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	destDir := t.TempDir()
	hash := sha256.Sum256(content)
	tmpPath, err := downloadBinary(context.Background(), srv.URL+"/agentapi-linux-amd64", destDir, hex.EncodeToString(hash[:]))
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpPath) }()

	// Verify the file was written to the correct directory.
	assert.Equal(t, destDir, filepath.Dir(tmpPath))

	// Verify contents.
	got, err := os.ReadFile(tmpPath)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	// Verify executable permission.
	info, err := os.Stat(tmpPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o111, "file should be executable")
}

func TestDownloadBinaryRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer srv.Close()

	_, err := downloadBinary(context.Background(), srv.URL, t.TempDir(), strings.Repeat("0", 64))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

func TestParseChecksumManifest(t *testing.T) {
	t.Parallel()
	want := strings.Repeat("a", 64)
	got, err := parseChecksumManifest([]byte(want+"  agentapi-linux-amd64\n"), "agentapi-linux-amd64")
	require.NoError(t, err)
	assert.Equal(t, want, got)

	_, err = parseChecksumManifest([]byte(want+"  other-file\n"), "agentapi-linux-amd64")
	require.Error(t, err)
}

func TestReplaceBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	currentPath := filepath.Join(dir, "agentapi")
	newPath := filepath.Join(dir, "agentapi-new")

	// Write the "old" binary.
	require.NoError(t, os.WriteFile(currentPath, []byte("old"), 0o755))
	// Write the "new" binary.
	require.NoError(t, os.WriteFile(newPath, []byte("new"), 0o755))

	require.NoError(t, replaceBinary(newPath, currentPath))

	// Verify the current binary has the new content.
	got, err := os.ReadFile(currentPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new"), got)
}
