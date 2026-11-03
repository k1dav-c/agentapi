package update

import (
	"context"
	"fmt"
	"os"
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

const (
	// checkTimeout bounds the lookup of the latest release.
	checkTimeout = time.Minute
	// installTimeout bounds the install: waiting for the lock another
	// update holds (bounded by the same timeout), the download, its check
	// and the rename. As POST /update's.
	installTimeout = 10 * time.Minute
)

func runUpdate(_ *cobra.Command, _ []string) error {
	checkCtx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	// Resolve current executable path.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	program, err := selfupdate.Locate(exePath)
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
	info, err := source.Latest(checkCtx)
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
		if restartFlag && !checkFlag {
			// The AgentAPIs running may be older than the program: another
			// update replaced it without restarting them.
			return restartAll(program)
		}
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

	// Download the binary, verified against the release's checksums, and
	// replace this one, under the lock that AgentAPIs updating through
	// their API take too.
	fmt.Printf("Downloading %s...\n", assetName)
	installCtx, cancelInstall := context.WithTimeout(context.Background(), installTimeout)
	defer cancelInstall()
	installed, err := source.Install(installCtx, info, program, forceFlag)
	if err != nil {
		return err
	}

	switch {
	case !installed:
		fmt.Printf("Another process installed v%s meanwhile.\n", latestVer.String())
	case currentVer != nil:
		fmt.Printf("Successfully updated agentapi from v%s to v%s\n", currentVer.String(), latestVer.String())
	default:
		fmt.Printf("Successfully updated agentapi to v%s\n", latestVer.String())
	}
	if restartFlag {
		return restartAll(program)
	}
	return nil
}

// restartAll restarts the AgentAPIs running program in place.
func restartAll(program selfupdate.Program) error {
	return restart.Run(context.Background(), restart.Options{All: true, Executable: program.Real, Out: os.Stdout})
}
