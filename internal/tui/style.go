// Package tui provides terminal UI primitives: styled output,
// interactive prompts, progress indicators, and dry-run annotations.
package tui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// ANSI color codes.
const (
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Dim       = "\033[2m"
	Red       = "\033[31m"
	Green     = "\033[32m"
	Yellow    = "\033[33m"
	Blue      = "\033[34m"
	Magenta   = "\033[35m"
	Cyan      = "\033[36m"
	White     = "\033[37m"
	BoldGreen = "\033[1;32m"
	BoldRed   = "\033[1;31m"
	BoldCyan  = "\033[1;36m"
	BoldYellow = "\033[1;33m"
)

// noColor disables ANSI codes when true.
var noColor bool

// SetNoColor disables or enables color output.
func SetNoColor(disable bool) {
	noColor = disable
}

func init() {
	// Auto-detect: disable color on Windows cmd (no ANSI), or when NO_COLOR is set.
	if os.Getenv("NO_COLOR") != "" {
		noColor = true
	}
	if runtime.GOOS == "windows" {
		// Windows Terminal and PowerShell support ANSI, but cmd.exe doesn't.
		if os.Getenv("WT_SESSION") == "" && os.Getenv("ConEmuPID") == "" {
			noColor = true
		}
	}
}

func c(color, text string) string {
	if noColor {
		return text
	}
	return color + text + Reset
}

// --- Status line printers ---

func Success(msg string) {
	fmt.Println(c(BoldGreen, "  ✓ ") + msg)
}

func Successf(format string, args ...any) {
	Success(fmt.Sprintf(format, args...))
}

func Fail(msg string) {
	fmt.Println(c(BoldRed, "  ✗ ") + msg)
}

func Failf(format string, args ...any) {
	Fail(fmt.Sprintf(format, args...))
}

func Warn(msg string) {
	fmt.Println(c(BoldYellow, "  ! ") + msg)
}

func Warnf(format string, args ...any) {
	Warn(fmt.Sprintf(format, args...))
}

func Info(msg string) {
	fmt.Println(c(BoldCyan, "  → ") + msg)
}

func Infof(format string, args ...any) {
	Info(fmt.Sprintf(format, args...))
}

func Step(n int, total int, msg string) {
	prefix := fmt.Sprintf("[%d/%d]", n, total)
	fmt.Println(c(Bold+Cyan, "  "+prefix+" ") + msg)
}

func DryRun(msg string) {
	fmt.Println(c(Dim+Yellow, "  [dry-run] ") + c(Dim, msg))
}

func DryRunf(format string, args ...any) {
	DryRun(fmt.Sprintf(format, args...))
}

// Header prints a section header.
func Header(title string) {
	line := strings.Repeat("─", 60)
	fmt.Println()
	fmt.Println(c(Bold+Cyan, "  "+title))
	fmt.Println(c(Dim, "  "+line))
}

// Banner prints the tool banner.
func Banner() {
	fmt.Println(c(Bold+Cyan, `
        _
   __ _| |_ ___
  / _`+"`"+` | __/ __|
 | (_| | |_\__ \
  \__,_|\__|___/

  Atlax Tooling Suite `)+c(Dim, "v0.1.0"))
	fmt.Println()
}

// Table prints a simple key-value table.
func Table(rows [][]string) {
	maxKey := 0
	for _, row := range rows {
		if len(row[0]) > maxKey {
			maxKey = len(row[0])
		}
	}
	for _, row := range rows {
		key := row[0]
		val := row[1]
		padding := strings.Repeat(" ", maxKey-len(key)+2)
		fmt.Printf("  %s%s%s\n", c(Bold, key), padding, val)
	}
}

// Divider prints a thin line.
func Divider() {
	fmt.Println(c(Dim, "  "+strings.Repeat("─", 60)))
}
