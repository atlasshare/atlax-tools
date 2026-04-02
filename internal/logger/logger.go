// Package logger provides structured, append-only logging to a file
// for every operation the tool performs. The log file serves as an
// audit trail and is set to read-only (0444) after each write.
package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry represents a single structured log entry.
type Entry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Command   string `json:"command"`
	Action    string `json:"action"`
	Detail    string `json:"detail,omitempty"`
	DryRun    bool   `json:"dry_run,omitempty"`
	Error     string `json:"error,omitempty"`
	Host      string `json:"host,omitempty"`
	OS        string `json:"os,omitempty"`
}

// Logger writes structured JSON log entries to a file.
type Logger struct {
	mu      sync.Mutex
	path    string
	command string
	dryRun  bool
}

var global *Logger

// Init creates the log file and sets the global logger.
// Log files are stored at: ~/.ats/logs/YYYY-MM-DD.jsonl
func Init(command string, dryRun bool, customPath string) error {
	logDir := customPath
	if logDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		logDir = filepath.Join(home, ".ats", "logs")
	}

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("cannot create log directory: %w", err)
	}

	filename := time.Now().Format("2006-01-02") + ".jsonl"
	path := filepath.Join(logDir, filename)

	// Make writable if it already exists (we set 0444 after each write).
	_ = os.Chmod(path, 0644)

	global = &Logger{
		path:    path,
		command: command,
		dryRun:  dryRun,
	}
	return nil
}

// Path returns the current log file path.
func Path() string {
	if global == nil {
		return ""
	}
	return global.path
}

// Log writes an INFO entry.
func Log(action, detail string) {
	write("INFO", action, detail, "")
}

// LogError writes an ERROR entry.
func LogError(action, detail string, err error) {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	write("ERROR", action, detail, errStr)
}

// LogWarn writes a WARN entry.
func LogWarn(action, detail string) {
	write("WARN", action, detail, "")
}

func write(level, action, detail, errMsg string) {
	if global == nil {
		return
	}

	global.mu.Lock()
	defer global.mu.Unlock()

	hostname, _ := os.Hostname()

	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Command:   global.command,
		Action:    action,
		Detail:    detail,
		DryRun:    global.dryRun,
		Error:     errMsg,
		Host:      hostname,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(global.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.Write(append(data, '\n'))

	// Set read-only after write.
	_ = os.Chmod(global.path, 0444)
}
