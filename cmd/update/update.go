package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Masterminds/semver/v3"
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
	RunE: runUpdate,
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
	// Resolve current executable path.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	return update(os.Stdout, selfupdate.Source{}, exePath, version.Version,
		options{force: forceFlag, check: checkFlag, restart: restartFlag})
}

// options are the flags of agentapi update.
type options struct {
	force, check, restart bool
}

// update updates the program at exePath, of version current, from
// source's latest release, and reports it to out.
func update(out io.Writer, source selfupdate.Source, exePath, current string, opts options) error {
	checkCtx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	program, err := selfupdate.Locate(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// Parse current version.
	currentVer, err := selfupdate.ParseVersion(current)
	if err != nil {
		if !opts.force {
			return fmt.Errorf("cannot determine current version (%s), use --force to update anyway: %w", current, err)
		}
		fmt.Fprintf(os.Stderr, "Warning: cannot parse current version %q, proceeding with --force\n", current)
	}

	// Fetch latest release.
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
		_, _ = fmt.Fprintf(out, "Current version: v%s\n", currentVer.String())
	} else {
		_, _ = fmt.Fprintf(out, "Current version: %s (unparseable)\n", current)
	}
	_, _ = fmt.Fprintf(out, "Latest version:  v%s\n", latestVer.String())

	// Compare versions. --force installs the latest regardless, but
	// --check reports what is.
	available := currentVer == nil || latestVer.GreaterThan(currentVer)

	if !available && (opts.check || !opts.force) {
		_, _ = fmt.Fprintln(out, "Already up to date.")
		if opts.restart && !opts.check {
			// The AgentAPIs running may be older than the program: another
			// update replaced it without restarting them.
			return restartAll(out, program)
		}
		return nil
	}

	if opts.check {
		_, _ = fmt.Fprintln(out, "Update available. Run 'agentapi update' to install.")
		return nil
	}

	if err := install(out, source, info, program, currentVer, latestVer, opts.force); err != nil {
		return err
	}
	if opts.restart {
		return restartAll(out, program)
	}
	return nil
}

// install downloads release info, the latest version, verified against
// the release's checksums, and replaces program with it, under the lock
// that AgentAPIs updating through their API take too.
func install(out io.Writer, source selfupdate.Source, info *selfupdate.Release, program selfupdate.Program, currentVer, latestVer *semver.Version, force bool) error {
	assetName := selfupdate.PlatformAsset()
	if _, err := info.Asset(assetName); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Downloading %s...\n", assetName)
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	installed, err := source.Install(ctx, info, program, force)
	if err != nil {
		return err
	}
	switch {
	case !installed:
		_, _ = fmt.Fprintf(out, "Another process installed v%s meanwhile.\n", latestVer.String())
	case currentVer != nil:
		_, _ = fmt.Fprintf(out, "Successfully updated agentapi from v%s to v%s\n", currentVer.String(), latestVer.String())
	default:
		_, _ = fmt.Fprintf(out, "Successfully updated agentapi to v%s\n", latestVer.String())
	}
	return nil
}

// restartAll restarts the AgentAPIs running program in place.
func restartAll(out io.Writer, program selfupdate.Program) error {
	return restart.Run(context.Background(), restart.Options{All: true, Executable: program.Real, Out: out})
}
