package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/coder/agentapi/cmd/restart"
	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/selfupdate"
	"github.com/spf13/cobra"
)

var (
	forceFlag   bool
	checkFlag   bool
	restartFlag bool
)

var UpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update agentapi to the latest version",
	Long: "Check for the latest release on GitHub and update the agentapi binary in place. " +
		"With --restart, the AgentAPIs running the binary then restart in place into the new version (see agentapi restart).",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate(cmd, args)
	},
}

func init() {
	UpdateCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Skip version comparison and update regardless")
	UpdateCmd.Flags().BoolVarP(&checkFlag, "check", "c", false, "Only check if an update is available")
	UpdateCmd.Flags().BoolVar(&restartFlag, "restart", false, "Then restart the AgentAPIs running this binary in place, the invoking user's under sudo")
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
	currentVer, err := selfupdate.ParseVersion(version.Version)
	if err != nil {
		if !forceFlag {
			return fmt.Errorf("cannot determine current version (%s), use --force to update anyway: %w", version.Version, err)
		}
		fmt.Fprintf(os.Stderr, "Warning: cannot parse current version %q, proceeding with --force\n", version.Version)
	}

	// Fetch latest release.
	var source selfupdate.Source
	info, err := source.Latest(ctx)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}

	latestVer, err := info.Version()
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
	assetName := selfupdate.PlatformAsset()
	if _, err := info.Asset(assetName); err != nil {
		return err
	}

	// Download the binary, verified against the release's checksums.
	fmt.Printf("Downloading %s...\n", assetName)
	tmpPath, err := source.Download(ctx, info, assetName, filepath.Dir(exePath))
	if err != nil {
		return err
	}
	// Clean up temp file on failure.
	defer func() {
		// If the file still exists at tmpPath, something went wrong — remove it.
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	// Replace the binary.
	if err := selfupdate.Replace(tmpPath, exePath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	if currentVer != nil {
		fmt.Printf("Successfully updated agentapi from v%s to v%s\n", currentVer.String(), latestVer.String())
	} else {
		fmt.Printf("Successfully updated agentapi to v%s\n", latestVer.String())
	}
	if restartFlag {
		return restart.Run(context.Background(), restart.Options{All: true, Executable: exePath, Out: os.Stdout})
	}
	return nil
}
