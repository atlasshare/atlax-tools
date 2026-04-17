package tui

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The prompt functions read from a package-level `reader` that is
// initialised from os.Stdin. These tests swap the reader for one
// backed by a string, run the assertion, then restore the original.
//
// reader mutation is serialised because all prompt funcs share it.
var promptMu sync.Mutex

func withInput(t *testing.T, input string, fn func()) {
	t.Helper()
	promptMu.Lock()
	defer promptMu.Unlock()

	orig := reader
	reader = bufio.NewReader(strings.NewReader(input))
	// Silence stdout noise emitted by the prompt.
	out := captureStdout(t, func() { fn() })
	_ = out
	reader = orig
}

func TestAsk_UsesDefaultOnEmpty(t *testing.T) {
	SetNoColor(true)
	var got string
	withInput(t, "\n", func() { got = Ask("label", "defaultval") })
	assert.Equal(t, "defaultval", got)
}

func TestAsk_ReturnsTrimmedInput(t *testing.T) {
	SetNoColor(true)
	var got string
	withInput(t, "  hello world  \n", func() { got = Ask("label", "def") })
	assert.Equal(t, "hello world", got)
}

func TestAsk_NoDefault(t *testing.T) {
	SetNoColor(true)
	var got string
	withInput(t, "value\n", func() { got = Ask("label", "") })
	assert.Equal(t, "value", got)
}

func TestAskRequired_LoopsUntilNonEmpty(t *testing.T) {
	SetNoColor(true)
	var got string
	withInput(t, "\n\n  \nfinally\n", func() { got = AskRequired("label") })
	assert.Equal(t, "finally", got)
}

func TestAskInt_AcceptsValidInteger(t *testing.T) {
	SetNoColor(true)
	var got int
	withInput(t, "42\n", func() { got = AskInt("label", 10) })
	assert.Equal(t, 42, got)
}

func TestAskInt_UsesDefaultOnEmpty(t *testing.T) {
	SetNoColor(true)
	var got int
	withInput(t, "\n", func() { got = AskInt("label", 7) })
	assert.Equal(t, 7, got)
}

func TestAskInt_LoopsOnInvalid(t *testing.T) {
	SetNoColor(true)
	var got int
	withInput(t, "not-a-number\n99\n", func() { got = AskInt("label", 0) })
	assert.Equal(t, 99, got)
}

func TestAskIntRange_EnforcesBounds(t *testing.T) {
	SetNoColor(true)
	var got int
	// 5 is out of [10,20], 15 is in range.
	withInput(t, "5\n15\n", func() { got = AskIntRange("label", 12, 10, 20) })
	assert.Equal(t, 15, got)
}

func TestAskPath_NonExistenceTolerated(t *testing.T) {
	SetNoColor(true)
	var got string
	withInput(t, "/no/such/path/anywhere\n", func() {
		got = AskPath("label", "/default", false)
	})
	assert.Equal(t, "/no/such/path/anywhere", got)
}

func TestAskPath_EmptyWithMustExistFalseReturnsDefault(t *testing.T) {
	SetNoColor(true)
	var got string
	// mustExist=false branch: empty input returns the default unchecked.
	withInput(t, "\n", func() {
		got = AskPath("label", "/default", false)
	})
	assert.Equal(t, "/default", got)
}

func TestAskPath_EmptyStringDefaultWithMustExist(t *testing.T) {
	SetNoColor(true)
	var got string
	// When default is empty and user enters empty, val=="" short-circuits
	// past the stat check.
	withInput(t, "\n", func() {
		got = AskPath("label", "", true)
	})
	assert.Equal(t, "", got)
}

func TestAskPath_ExistenceCheck(t *testing.T) {
	SetNoColor(true)
	dir := t.TempDir()
	existing := filepath.Join(dir, "present")
	require.NoError(t, os.WriteFile(existing, []byte("x"), 0o644))

	var got string
	// First attempt non-existent path, second attempt the real one.
	withInput(t, "/not/a/real/path\n"+existing+"\n", func() {
		got = AskPath("label", "", true)
	})
	assert.Equal(t, existing, got)
}

func TestConfirm_DefaultYesOnEmpty(t *testing.T) {
	SetNoColor(true)
	var got bool
	withInput(t, "\n", func() { got = Confirm("label", true) })
	assert.True(t, got)
}

func TestConfirm_DefaultNoOnEmpty(t *testing.T) {
	SetNoColor(true)
	var got bool
	withInput(t, "\n", func() { got = Confirm("label", false) })
	assert.False(t, got)
}

func TestConfirm_ExplicitYes(t *testing.T) {
	SetNoColor(true)
	var got bool
	withInput(t, "y\n", func() { got = Confirm("label", false) })
	assert.True(t, got)
}

func TestConfirm_ExplicitYesSpelledOut(t *testing.T) {
	SetNoColor(true)
	var got bool
	withInput(t, "YES\n", func() { got = Confirm("label", false) })
	assert.True(t, got)
}

func TestConfirm_ExplicitNo(t *testing.T) {
	SetNoColor(true)
	var got bool
	withInput(t, "n\n", func() { got = Confirm("label", true) })
	assert.False(t, got)
}

func TestSelect_ReturnsIndex(t *testing.T) {
	SetNoColor(true)
	opts := []string{"alpha", "beta", "gamma"}
	var got int
	withInput(t, "2\n", func() { got = Select("label", opts, 0) })
	assert.Equal(t, 1, got, "input '2' means index 1 (second option)")
}

func TestSelect_DefaultOnEmpty(t *testing.T) {
	SetNoColor(true)
	opts := []string{"alpha", "beta"}
	var got int
	withInput(t, "\n", func() { got = Select("label", opts, 1) })
	assert.Equal(t, 1, got)
}

func TestSelect_LoopsOnOutOfRange(t *testing.T) {
	SetNoColor(true)
	opts := []string{"alpha", "beta"}
	var got int
	withInput(t, "5\n0\n1\n", func() { got = Select("label", opts, 0) })
	assert.Equal(t, 0, got)
}

func TestSelectString(t *testing.T) {
	SetNoColor(true)
	opts := []string{"alpha", "beta", "gamma"}
	var got string
	withInput(t, "3\n", func() { got = SelectString("label", opts, 0) })
	assert.Equal(t, "gamma", got)
}

func TestAskMultiSelect_Empty(t *testing.T) {
	SetNoColor(true)
	opts := []string{"alpha", "beta"}
	var got []string
	withInput(t, "\n", func() { got = AskMultiSelect("label", opts) })
	assert.Nil(t, got)
}

func TestAskMultiSelect_Parses(t *testing.T) {
	SetNoColor(true)
	opts := []string{"alpha", "beta", "gamma"}
	var got []string
	// "1, 3, 99" -> alpha, gamma; the out-of-range 99 is silently dropped.
	withInput(t, "1, 3, 99, abc\n", func() { got = AskMultiSelect("label", opts) })
	assert.Equal(t, []string{"alpha", "gamma"}, got)
}

func TestAskPassword(t *testing.T) {
	SetNoColor(true)
	var got string
	withInput(t, "  secret  \n", func() { got = AskPassword("label") })
	assert.Equal(t, "secret", got)
}
