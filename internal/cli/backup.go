package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/atlasshare/atlax-tools/internal/logger"
	"github.com/atlasshare/atlax-tools/internal/tui"
)

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup and restore atlax configs and certificates",
	}
	cmd.AddCommand(newBackupCreateCmd(), newBackupRestoreCmd())
	return cmd
}

// ---------- backup create ----------

func newBackupCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create",
		Short: "Create a backup archive of configs and certs",
		RunE:  runBackupCreate,
	}
}

func runBackupCreate(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Backup Create")

	configDir := tui.Ask("Config directory to backup", platInfo.ConfigBasePath())
	backupDir := tui.Ask("Backup destination directory", filepath.Join(os.Getenv("HOME"), ".ats", "backups"))

	includeKeys := tui.Confirm("Include private keys in backup?", true)

	// List what will be backed up.
	var files []string
	err := filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if info.IsDir() {
			return nil
		}
		if !includeKeys && strings.HasSuffix(info.Name(), ".key") {
			tui.Warnf("Skipping private key: %s", info.Name())
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("cannot scan %s: %w", configDir, err)
	}

	if len(files) == 0 {
		tui.Warnf("No files found in %s", configDir)
		return nil
	}

	tui.Infof("Files to backup: %d", len(files))
	for _, f := range files {
		rel, _ := filepath.Rel(configDir, f)
		tui.Infof("  %s", rel)
	}

	if !tui.Confirm("Create backup?", true) {
		tui.Warn("Aborted.")
		return nil
	}

	// Create archive.
	timestamp := time.Now().Format("20060102-150405")
	hostname, _ := os.Hostname()
	archiveName := fmt.Sprintf("atlax-backup-%s-%s.tar.gz", hostname, timestamp)
	archivePath := filepath.Join(backupDir, archiveName)

	if dryRun {
		tui.DryRunf("Would create %s with %d files", archivePath, len(files))
		return nil
	}

	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return fmt.Errorf("cannot create backup directory: %w", err)
	}

	f, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("cannot create archive: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, filePath := range files {
		relPath, _ := filepath.Rel(configDir, filePath)
		if err := addToArchive(tw, filePath, relPath); err != nil {
			tui.Warnf("Cannot add %s: %s", relPath, err)
			continue
		}
	}

	// Set archive to read-only.
	_ = os.Chmod(archivePath, 0444)

	tui.Successf("Backup created: %s", archivePath)
	logger.Log("backup-create", archivePath)

	// Show size.
	if info, err := os.Stat(archivePath); err == nil {
		tui.Infof("Size: %s", humanizeBytes(info.Size()))
	}

	return nil
}

// ---------- backup restore ----------

func newBackupRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Restore configs and certs from a backup archive",
		RunE:  runBackupRestore,
	}
}

func runBackupRestore(cmd *cobra.Command, args []string) error {
	tui.Banner()
	tui.Header("Backup Restore")

	archivePath := ""
	if len(args) > 0 {
		archivePath = args[0]
	} else {
		// List available backups.
		backupDir := filepath.Join(os.Getenv("HOME"), ".ats", "backups")
		entries, err := os.ReadDir(backupDir)
		if err == nil && len(entries) > 0 {
			var archives []string
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".tar.gz") {
					archives = append(archives, filepath.Join(backupDir, e.Name()))
				}
			}
			if len(archives) > 0 {
				tui.Infof("Available backups:")
				archivePath = tui.SelectString("Select backup", archives, len(archives)-1)
			}
		}
		if archivePath == "" {
			archivePath = tui.AskRequired("Backup archive path")
		}
	}

	restoreDir := tui.Ask("Restore destination", platInfo.ConfigBasePath())

	// Preview contents.
	contents, err := listArchiveContents(archivePath)
	if err != nil {
		return fmt.Errorf("cannot read archive: %w", err)
	}

	tui.Infof("Archive contains %d files:", len(contents))
	for _, c := range contents {
		tui.Infof("  %s (%s)", c.name, humanizeBytes(c.size))
	}

	if !tui.Confirm(fmt.Sprintf("Restore to %s? (existing files will be overwritten)", restoreDir), false) {
		tui.Warn("Aborted.")
		return nil
	}

	if dryRun {
		tui.DryRunf("Would extract %d files to %s", len(contents), restoreDir)
		return nil
	}

	// Extract.
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	restored := 0

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		targetPath := filepath.Join(restoreDir, hdr.Name)

		// Security: prevent path traversal.
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(restoreDir)) {
			tui.Warnf("Skipping suspicious path: %s", hdr.Name)
			continue
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
			continue
		}

		// Create parent directories.
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}

		if _, err := io.Copy(outFile, tr); err != nil {
			outFile.Close()
			return err
		}
		outFile.Close()

		tui.Successf("Restored %s", hdr.Name)
		restored++
	}

	tui.Header("Restore Complete")
	tui.Successf("Restored %d files to %s", restored, restoreDir)
	logger.Log("backup-restore", fmt.Sprintf("%s → %s (%d files)", archivePath, restoreDir, restored))

	tui.Header("Next Steps")
	tui.Infof("1. Verify config files: cat %s/*.yaml", restoreDir)
	tui.Infof("2. Restart services: sudo systemctl restart atlax-relay atlax-agent")

	return nil
}

// --- helpers ---

type archiveEntry struct {
	name string
	size int64
}

func addToArchive(tw *tar.Writer, srcPath, relPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = relPath

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(tw, f)
	return err
}

func listArchiveContents(archivePath string) ([]archiveEntry, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var entries []archiveEntry

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeDir {
			entries = append(entries, archiveEntry{name: hdr.Name, size: hdr.Size})
		}
	}

	return entries, nil
}

func humanizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
