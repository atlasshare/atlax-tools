// Package cli defines all cobra commands for the ats CLI.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/platform"
	"github.com/atlasshare/atlax-tools/internal/tui"
	"github.com/atlasshare/atlax-tools/internal/version"
)

// Global flags available to all commands.
var (
	dryRun  bool
	noColor bool
	verbose bool
	logFile string
)

// platInfo is lazily detected platform info.
var platInfo platform.Info

// NewRoot creates the root command with all subcommands attached.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "ats",
		Short: "Atlax Tooling Suite — interactive provisioning, certificate management, and diagnostics",
		Long: `ats (Atlax Tooling Suite) is a Go CLI for deploying, configuring,
and maintaining Atlax relay and agent nodes. All commands are fully
interactive — the operator provides every parameter through guided prompts.

Supports Linux (Ubuntu, Debian, RHEL, Arch, Alpine), macOS, FreeBSD, and Windows.`,
		Version: version.ToolVersion,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			tui.SetNoColor(noColor)

			// Detect platform.
			platInfo = platform.Detect()

			// Initialize logger.
			cmdName := cmd.Name()
			if parent := cmd.Parent(); parent != nil && parent.Name() != "ats" {
				cmdName = parent.Name() + "-" + cmdName
			}
			if err := logger.Init(cmdName, dryRun, logFile); err != nil {
				return fmt.Errorf("cannot initialize logger: %w", err)
			}

			if verbose {
				tui.Banner()
				tui.Header("Platform Detection")
				tui.Table([][]string{
					{"OS", platInfo.OS.String()},
					{"Version", platInfo.OSVersion},
					{"Arch", platInfo.Arch},
					{"Init System", platInfo.InitSystem.String()},
					{"Package Manager", platInfo.PackageManager.String()},
					{"Firewall", platInfo.Firewall.String()},
					{"Docker", boolStr(platInfo.HasDocker)},
					{"OpenSSL", boolStr(platInfo.HasOpenSSL)},
					{"step CLI", boolStr(platInfo.HasStepCLI)},
					{"Root", boolStr(platInfo.IsRoot)},
				})
				tui.Infof("Log file: %s", logger.Path())
				fmt.Println()
			}

			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags.
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Show what would happen without making changes")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed platform info and progress")
	root.PersistentFlags().StringVar(&logFile, "log-dir", "", "Custom log directory (default: ~/.ats/logs/)")

	// Attach subcommands.
	root.AddCommand(
		newSetupCmd(),
		newCertsCmd(),
		newCustomerCmd(),
		newServiceCmd(),
		newHealthCmd(),
		newBackupCmd(),
		newUninstallCmd(),
	)

	return root
}

// Execute runs the root command.
func Execute() {
	root := NewRoot()
	if err := root.Execute(); err != nil {
		tui.Failf("%s", err)
		os.Exit(1)
	}
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
