package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var stdoutCaptureMu sync.Mutex

// captureStdout redirects os.Stdout for the duration of fn and returns
// the captured bytes. Calls are serialized because os.Stdout is a
// process-wide global and parallel capture would interleave writes.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	stdoutCaptureMu.Lock()
	defer stdoutCaptureMu.Unlock()

	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()

	// Close the writer so the copier sees EOF and exits. Then restore.
	_ = w.Close()
	<-done
	os.Stdout = orig
	_ = r.Close()
	return buf.String()
}

func TestSetNoColor_OnOff(t *testing.T) {
	// Can't be parallel: toggles package-global noColor.
	prev := noColor
	t.Cleanup(func() { noColor = prev })

	SetNoColor(true)
	assert.Equal(t, "plain", c(Red, "plain"), "noColor=true returns text unchanged")

	SetNoColor(false)
	colored := c(Red, "plain")
	assert.Contains(t, colored, Red)
	assert.Contains(t, colored, Reset)
}

func TestStatusLinePrinters(t *testing.T) {
	// Can't be parallel: captures os.Stdout.
	SetNoColor(true)

	cases := []struct {
		name    string
		fn      func()
		wantSub string
	}{
		{"Success", func() { Success("ok") }, "ok"},
		{"Successf", func() { Successf("ok-%d", 1) }, "ok-1"},
		{"Fail", func() { Fail("bad") }, "bad"},
		{"Failf", func() { Failf("bad-%d", 2) }, "bad-2"},
		{"Warn", func() { Warn("hmm") }, "hmm"},
		{"Warnf", func() { Warnf("hmm-%d", 3) }, "hmm-3"},
		{"Info", func() { Info("fyi") }, "fyi"},
		{"Infof", func() { Infof("fyi-%d", 4) }, "fyi-4"},
		{"DryRun", func() { DryRun("would do") }, "would do"},
		{"DryRunf", func() { DryRunf("would do %d", 5) }, "would do 5"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, tc.fn)
			assert.Contains(t, out, tc.wantSub)
		})
	}
}

func TestStep(t *testing.T) {
	SetNoColor(true)
	out := captureStdout(t, func() { Step(2, 5, "doing thing") })
	assert.Contains(t, out, "[2/5]")
	assert.Contains(t, out, "doing thing")
}

func TestHeader(t *testing.T) {
	SetNoColor(true)
	out := captureStdout(t, func() { Header("Section") })
	assert.Contains(t, out, "Section")
	// Horizontal rule.
	assert.Contains(t, out, "─")
}

func TestBanner(t *testing.T) {
	SetNoColor(true)
	out := captureStdout(t, func() { Banner() })
	assert.Contains(t, out, "Atlax Tooling Suite")
}

func TestTable(t *testing.T) {
	SetNoColor(true)
	rows := [][]string{
		{"Name", "Alice"},
		{"Role", "Engineer"},
	}
	out := captureStdout(t, func() { Table(rows) })
	assert.Contains(t, out, "Name")
	assert.Contains(t, out, "Alice")
	assert.Contains(t, out, "Role")
	assert.Contains(t, out, "Engineer")
}

func TestDivider(t *testing.T) {
	SetNoColor(true)
	out := captureStdout(t, func() { Divider() })
	assert.Contains(t, out, "─")
}

func TestColorHelper_WhenColorEnabled(t *testing.T) {
	prev := noColor
	t.Cleanup(func() { noColor = prev })

	noColor = false
	got := c(Green, "text")
	assert.True(t, strings.HasPrefix(got, Green))
	assert.True(t, strings.HasSuffix(got, Reset))
	assert.Contains(t, got, "text")
}
