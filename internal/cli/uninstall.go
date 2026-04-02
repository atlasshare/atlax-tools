package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/platform"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove atlax relay or agent from this machine",
		RunE:  runUninstall,
	}
}

func runUninstall(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Atlax Uninstall")

	info := platInfo

	nodeType := tui.Select("What to uninstall", []string{
		"Relay",
		"Agent",
		"Both",
	}, 0)

	removeBinaries := tui.Confirm("Remove binaries?", true)
	removeConfig := tui.Confirm("Remove configuration files?", false)
	removeCerts := tui.Confirm("Remove certificates?", false)
	removeLogs := tui.Confirm("Remove log files?", false)
	removeUser := tui.Confirm("Remove 'atlax' system user?", false)
	removeGroup := tui.Confirm("Remove 'atlax' system group?", false)

	if removeCerts {
		tui.Warnf("Removing certificates is destructive — agents will lose connectivity!")
		if !tui.Confirm("Are you sure you want to remove certificates?", false) {
			removeCerts = false
		}
	}

	// Summary
	tui.Header("Actions")
	actions := []string{}

	isRelay := nodeType == 0 || nodeType == 2
	isAgent := nodeType == 1 || nodeType == 2

	if isRelay {
		actions = append(actions, "Stop and disable atlax-relay service")
		if removeBinaries {
			actions = append(actions, "Remove /usr/local/bin/atlax-relay")
		}
	}
	if isAgent {
		actions = append(actions, "Stop and disable atlax-agent service")
		if removeBinaries {
			actions = append(actions, "Remove /usr/local/bin/atlax-agent")
		}
	}
	if removeConfig {
		actions = append(actions, fmt.Sprintf("Remove %s/*.yaml", info.ConfigBasePath()))
	}
	if removeCerts {
		actions = append(actions, fmt.Sprintf("Remove %s/certs/", info.ConfigBasePath()))
	}
	if removeLogs {
		actions = append(actions, fmt.Sprintf("Remove %s/", info.LogBasePath()))
	}
	if removeUser {
		actions = append(actions, "Remove system user 'atlax'")
	}
	if removeGroup {
		actions = append(actions, "Remove system group 'atlax'")
	}

	for _, a := range actions {
		tui.Infof("  %s", a)
	}

	if !tui.Confirm("Proceed with uninstall? This cannot be undone.", false) {
		tui.Warn("Aborted.")
		return nil
	}

	// Offer backup first
	if removeConfig || removeCerts {
		if tui.Confirm("Create a backup before proceeding?", true) {
			tui.Infof("Run: ats backup create")
			runBackupCreate(cmd, nil)
		}
	}

	// --- Execute ---
	totalSteps := len(actions)
	step := 0

	// Stop services
	if isRelay && info.InitSystem == platform.Systemd {
		step++
		tui.Step(step, totalSteps, "Stopping atlax-relay")
		execOrDry(dryRun, "sudo", "systemctl", "stop", "atlax-relay")
		execOrDry(dryRun, "sudo", "systemctl", "disable", "atlax-relay")
		execOrDry(dryRun, "sudo", "rm", "-f", "/etc/systemd/system/atlax-relay.service")
		execOrDry(dryRun, "sudo", "systemctl", "daemon-reload")
		logger.Log("uninstall", "stopped atlax-relay")
	}

	if isAgent && info.InitSystem == platform.Systemd {
		step++
		tui.Step(step, totalSteps, "Stopping atlax-agent")
		execOrDry(dryRun, "sudo", "systemctl", "stop", "atlax-agent")
		execOrDry(dryRun, "sudo", "systemctl", "disable", "atlax-agent")
		execOrDry(dryRun, "sudo", "rm", "-f", "/etc/systemd/system/atlax-agent.service")
		execOrDry(dryRun, "sudo", "systemctl", "daemon-reload")
		logger.Log("uninstall", "stopped atlax-agent")
	}

	// Remove binaries
	if removeBinaries {
		step++
		tui.Step(step, totalSteps, "Removing binaries")
		if isRelay {
			removeFile(dryRun, "/usr/local/bin/atlax-relay")
		}
		if isAgent {
			removeFile(dryRun, "/usr/local/bin/atlax-agent")
		}
	}

	// Remove config
	if removeConfig {
		step++
		tui.Step(step, totalSteps, "Removing configuration")
		configDir := info.ConfigBasePath()
		files := []string{"relay.yaml", "agent.yaml"}
		for _, f := range files {
			removeFile(dryRun, fmt.Sprintf("%s/%s", configDir, f))
		}
		// Remove backup configs too
		removeFile(dryRun, fmt.Sprintf("%s/relay.bak.yaml", configDir))
		removeFile(dryRun, fmt.Sprintf("%s/agent.bak.yaml", configDir))
	}

	// Remove certs
	if removeCerts {
		step++
		tui.Step(step, totalSteps, "Removing certificates")
		certDir := fmt.Sprintf("%s/certs", info.ConfigBasePath())
		removeDir(dryRun, certDir)
	}

	// Remove logs
	if removeLogs {
		step++
		tui.Step(step, totalSteps, "Removing logs")
		removeDir(dryRun, info.LogBasePath())
	}

	// Remove user
	if removeUser {
		step++
		tui.Step(step, totalSteps, "Removing system user")
		execOrDry(dryRun, "sudo", "userdel", "atlax")
	}

	// Remove group
	if removeGroup {
		step++
		tui.Step(step, totalSteps, "Removing system group")
		execOrDry(dryRun, "sudo", "groupdel", "atlax")
	}

	tui.Header("Uninstall Complete")
	tui.Successf("Atlax has been removed from this machine")
	logger.Log("uninstall", "complete")

	return nil
}

func execOrDry(dry bool, name string, args ...string) {
	cmdStr := name + " " + strings.Join(args, " ")
	if dry {
		tui.DryRunf("%s", cmdStr)
		return
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Run(); err != nil {
		tui.Warnf("%s: %s", cmdStr, err)
	} else {
		tui.Successf("%s", cmdStr)
	}
}

func removeFile(dry bool, path string) {
	if dry {
		tui.DryRunf("rm %s", path)
		return
	}
	if err := os.Remove(path); err != nil {
		if !os.IsNotExist(err) {
			// Try with sudo
			cmd := exec.Command("sudo", "rm", "-f", path)
			if err := cmd.Run(); err != nil {
				tui.Warnf("Cannot remove %s: %s", path, err)
				return
			}
		}
	}
	tui.Successf("Removed %s", path)
	logger.Log("remove", path)
}

func removeDir(dry bool, path string) {
	if dry {
		tui.DryRunf("rm -rf %s", path)
		return
	}
	cmd := exec.Command("sudo", "rm", "-rf", path)
	if err := cmd.Run(); err != nil {
		tui.Warnf("Cannot remove %s: %s", path, err)
		return
	}
	tui.Successf("Removed %s", path)
	logger.Log("remove-dir", path)
}
