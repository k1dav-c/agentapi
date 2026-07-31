package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/coder/agentapi/internal/version"
	"github.com/spf13/cobra"
)

const (
	repoOwner = "k1dav-c"
	repoName  = "agentapi"
)

// apiBaseURL is the GitHub API base URL. It is a variable so tests can
// override it with an httptest server.
var apiBaseURL = "https://api.github.com"

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

var (
	forceFlag bool
	checkFlag bool
)

var UpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update agentapi to the latest version",
	Long:  `Check for the latest release on GitHub and update the agentapi binary in place.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate(cmd, args)
	},
}

func init() {
	UpdateCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Skip version comparison and update regardless")
	UpdateCmd.Flags().BoolVarP(&checkFlag, "check", "c", false, "Only check if an update is available")
}

func fetchLatestRelease(ctx context.Context) (*releaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBaseURL, repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "agentapi-update")
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

	var info releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode release info: %w", err)
	}
	return &info, nil
}

func parseVersion(v string) (*semver.Version, error) {
	sv, err := semver.NewVersion(v)
	if err != nil {
		return nil, fmt.Errorf("parse version %q: %w", v, err)
	}
	return sv, nil
}

func binaryAssetName() string {
	name := fmt.Sprintf("agentapi-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// buildAssetName builds an asset name for a given os/arch pair. This is
// separated from binaryAssetName so tests can exercise arbitrary combinations.
func buildAssetName(goos, goarch string) string {
	name := fmt.Sprintf("agentapi-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func findAssetURL(info *releaseInfo, assetName string) (string, error) {
	for _, a := range info.Assets {
		if a.Name == assetName {
			return a.BrowserDownloadURL, nil
		}
	}
	available := make([]string, 0, len(info.Assets))
	for _, a := range info.Assets {
		available = append(available, a.Name)
	}
	return "", fmt.Errorf("no release binary %q found in release %s (available: %v)", assetName, info.TagName, available)
}

func downloadBinary(ctx context.Context, url, destDir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "agentapi-update")

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

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write downloaded binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("set executable permission: %w", err)
	}

	return tmpPath, nil
}

func runUpdate(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Resolve current executable path.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// Parse current version.
	currentVer, err := parseVersion(version.Version)
	if err != nil {
		if !forceFlag {
			return fmt.Errorf("cannot determine current version (%s), use --force to update anyway: %w", version.Version, err)
		}
		fmt.Fprintf(os.Stderr, "Warning: cannot parse current version %q, proceeding with --force\n", version.Version)
	}

	// Fetch latest release.
	info, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}

	latestVer, err := parseVersion(info.TagName)
	if err != nil {
		return fmt.Errorf("parse latest release version: %w", err)
	}

	// Print versions.
	if currentVer != nil {
		fmt.Printf("Current version: v%s\n", currentVer.String())
	} else {
		fmt.Printf("Current version: %s (unparseable)\n", version.Version)
	}
	fmt.Printf("Latest version:  v%s\n", latestVer.String())

	// Compare versions.
	needsUpdate := forceFlag || currentVer == nil || latestVer.GreaterThan(currentVer)

	if !needsUpdate {
		fmt.Println("Already up to date.")
		return nil
	}

	if checkFlag {
		fmt.Println("Update available. Run 'agentapi update' to install.")
		return nil
	}

	// Find the asset for this platform.
	assetName := binaryAssetName()
	assetURL, err := findAssetURL(info, assetName)
	if err != nil {
		return err
	}

	fmt.Printf("Downloading %s...\n", assetName)

	// Download the binary.
	tmpPath, err := downloadBinary(ctx, assetURL, filepath.Dir(exePath))
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	// Clean up temp file on failure.
	defer func() {
		// If the file still exists at tmpPath, something went wrong — remove it.
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	// Replace the binary.
	if err := replaceBinary(tmpPath, exePath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	if currentVer != nil {
		fmt.Printf("Successfully updated agentapi from v%s to v%s\n", currentVer.String(), latestVer.String())
	} else {
		fmt.Printf("Successfully updated agentapi to v%s\n", latestVer.String())
	}
	return nil
}
