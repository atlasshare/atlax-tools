package tui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var reader = bufio.NewReader(os.Stdin)

// Ask prompts for a string value with an optional default.
func Ask(label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s %s [%s]: ", c(Bold, "?"), label, c(Dim, defaultVal))
	} else {
		fmt.Printf("  %s %s: ", c(Bold, "?"), label)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

// AskRequired prompts for a required string (loops until non-empty).
func AskRequired(label string) string {
	for {
		val := Ask(label, "")
		if val != "" {
			return val
		}
		Warn("This field is required.")
	}
}

// AskInt prompts for an integer with a default.
func AskInt(label string, defaultVal int) int {
	for {
		raw := Ask(label, strconv.Itoa(defaultVal))
		n, err := strconv.Atoi(raw)
		if err == nil {
			return n
		}
		Warn("Please enter a valid number.")
	}
}

// AskIntRange prompts for an integer within [min, max].
func AskIntRange(label string, defaultVal, min, max int) int {
	for {
		n := AskInt(fmt.Sprintf("%s (%d-%d)", label, min, max), defaultVal)
		if n >= min && n <= max {
			return n
		}
		Warnf("Value must be between %d and %d.", min, max)
	}
}

// AskPath prompts for a file/directory path with optional existence check.
func AskPath(label, defaultVal string, mustExist bool) string {
	for {
		val := Ask(label, defaultVal)
		if !mustExist || val == "" {
			return val
		}
		if _, err := os.Stat(val); err == nil {
			return val
		}
		Warnf("Path %q does not exist.", val)
	}
}

// Confirm asks a yes/no question. Default is the provided bool.
func Confirm(label string, defaultYes bool) bool {
	suffix := "y/N"
	if defaultYes {
		suffix = "Y/n"
	}
	fmt.Printf("  %s %s [%s]: ", c(Bold, "?"), label, c(Dim, suffix))
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))

	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

// Select presents a list of options and returns the selected index.
func Select(label string, options []string, defaultIdx int) int {
	fmt.Printf("  %s %s:\n", c(Bold, "?"), label)
	for i, opt := range options {
		marker := "  "
		if i == defaultIdx {
			marker = c(Cyan, "▸ ")
		}
		fmt.Printf("    %s%s\n", marker, opt)
	}
	for {
		raw := Ask("Enter number (1-"+strconv.Itoa(len(options))+")", strconv.Itoa(defaultIdx+1))
		n, err := strconv.Atoi(raw)
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
		Warnf("Please enter a number between 1 and %d.", len(options))
	}
}

// SelectString presents options and returns the selected string value.
func SelectString(label string, options []string, defaultIdx int) string {
	idx := Select(label, options, defaultIdx)
	return options[idx]
}

// AskMultiSelect lets the operator toggle multiple options on/off.
func AskMultiSelect(label string, options []string) []string {
	fmt.Printf("  %s %s (comma-separated numbers):\n", c(Bold, "?"), label)
	for i, opt := range options {
		fmt.Printf("    %d. %s\n", i+1, opt)
	}
	raw := Ask("Select", "")
	if raw == "" {
		return nil
	}

	var selected []string
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && n >= 1 && n <= len(options) {
			selected = append(selected, options[n-1])
		}
	}
	return selected
}

// AskPassword reads input (note: terminal echo is NOT suppressed for
// portability; use this for non-secret interactive values).
func AskPassword(label string) string {
	fmt.Printf("  %s %s: ", c(Bold, "?"), label)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}
