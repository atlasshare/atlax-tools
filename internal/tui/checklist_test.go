package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withHome points os.UserHomeDir to a temporary directory for the scope
// of a subtest so that Checklist persists checkpoints there instead of
// the real home. It restores HOME on cleanup.
//
// It also disables color output so that the output comparison routines
// in the tests are deterministic.
func withHome(t *testing.T, home string) {
	t.Helper()
	SetNoColor(true)
	orig, hadHome := os.LookupEnv("HOME")
	require.NoError(t, os.Setenv("HOME", home))
	// On some platforms USERPROFILE is consulted by os.UserHomeDir.
	origUP, hadUP := os.LookupEnv("USERPROFILE")
	require.NoError(t, os.Setenv("USERPROFILE", home))

	t.Cleanup(func() {
		if hadHome {
			_ = os.Setenv("HOME", orig)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadUP {
			_ = os.Setenv("USERPROFILE", origUP)
		} else {
			_ = os.Unsetenv("USERPROFILE")
		}
	})
}

func newSteps() []ChecklistStep {
	return []ChecklistStep{
		{ID: "step1", Label: "First step", Status: Pending},
		{ID: "step2", Label: "Second step", Status: Pending},
		{ID: "step3", Label: "Third step", Status: Pending},
	}
}

// --- State transitions ---

func TestChecklist_StateTransitions_Done(t *testing.T) {
	withHome(t, t.TempDir())

	cl := NewChecklist("test-done", newSteps())

	// All start pending.
	assert.False(t, cl.IsDone("step1"))
	assert.False(t, cl.IsSkipped("step1"))
	assert.False(t, cl.IsComplete())

	cl.MarkInProgress("step1")
	assert.Equal(t, InProgress, cl.steps[0].Status)
	assert.False(t, cl.IsDone("step1"))

	cl.MarkDone("step1")
	assert.True(t, cl.IsDone("step1"))
	assert.Equal(t, Done, cl.steps[0].Status)

	cl.MarkDone("step2")
	cl.MarkSkipped("step3")
	assert.True(t, cl.IsSkipped("step3"))
	assert.True(t, cl.IsComplete(), "all done-or-skipped should be complete")
}

func TestChecklist_StateTransitions_Failed(t *testing.T) {
	withHome(t, t.TempDir())

	cl := NewChecklist("test-failed", newSteps())
	cl.MarkInProgress("step1")
	cl.MarkFailed("step1", errors.New("boom"))

	assert.Equal(t, Failed, cl.steps[0].Status)
	assert.Equal(t, "boom", cl.steps[0].Error)
	assert.False(t, cl.IsDone("step1"))
	assert.False(t, cl.IsComplete())
}

func TestChecklist_MarkFailed_NilError(t *testing.T) {
	withHome(t, t.TempDir())

	cl := NewChecklist("test-nil-err", newSteps())
	cl.MarkFailed("step1", nil)

	assert.Equal(t, Failed, cl.steps[0].Status)
	assert.Empty(t, cl.steps[0].Error, "nil error must render as empty string")
}

func TestChecklist_IsDone_UnknownStepID(t *testing.T) {
	withHome(t, t.TempDir())

	cl := NewChecklist("test-unknown", newSteps())
	assert.False(t, cl.IsDone("does-not-exist"))
	assert.False(t, cl.IsSkipped("does-not-exist"))
}

func TestChecklist_SetStatus_UnknownStepID(t *testing.T) {
	withHome(t, t.TempDir())

	cl := NewChecklist("test-set-unknown", newSteps())

	// Mutating an unknown ID is a silent no-op — none of the existing
	// steps should change state.
	cl.MarkDone("ghost")
	for _, s := range cl.steps {
		assert.Equal(t, Pending, s.Status)
	}
}

// --- Checkpoint save/load ---

func TestChecklist_CheckpointFileWrittenOnTransition(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	cl := NewChecklist("save-test", newSteps())
	cl.MarkInProgress("step1")

	cpPath := filepath.Join(home, ".ats", "checkpoints", "save-test.json")
	data, err := os.ReadFile(cpPath)
	require.NoError(t, err, "checkpoint file must be written on each mutation")

	var cp Checkpoint
	require.NoError(t, json.Unmarshal(data, &cp))
	assert.Equal(t, "save-test", cp.Command)
	require.Len(t, cp.Steps, 3)
	assert.Equal(t, InProgress, cp.Steps[0].Status)
}

func TestChecklist_ResumeFromCheckpoint(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	// First run: complete step1, mark step2 as in-progress then fail.
	first := NewChecklist("resume-test", newSteps())
	first.MarkDone("step1")
	first.MarkInProgress("step2")
	first.MarkFailed("step2", errors.New("transient"))

	// Second run: fresh Checklist with identical step IDs.
	second := NewChecklist("resume-test", newSteps())
	cp, ok := second.HasPrevious()
	require.True(t, ok, "should detect incomplete previous run")
	require.NotNil(t, cp)

	second.Resume(cp)

	// step1 was done — should carry over.
	assert.Equal(t, Done, second.steps[0].Status, "done step must be restored")
	// step2 was failed (not done, not skipped) — should NOT carry over,
	// so the operator retries it.
	assert.Equal(t, Pending, second.steps[1].Status, "failed step must be retried on resume")
	// step3 never ran — stays pending.
	assert.Equal(t, Pending, second.steps[2].Status)
}

func TestChecklist_ResumeAfterSkippedAreRestored(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	first := NewChecklist("resume-skipped", newSteps())
	first.MarkDone("step1")
	first.MarkSkipped("step2")
	// step3 left pending so the checkpoint isn't deleted by Summary.

	second := NewChecklist("resume-skipped", newSteps())
	cp, ok := second.HasPrevious()
	require.True(t, ok)
	second.Resume(cp)

	assert.Equal(t, Done, second.steps[0].Status)
	assert.Equal(t, Skipped, second.steps[1].Status)
	assert.Equal(t, Pending, second.steps[2].Status)
}

func TestChecklist_HasPrevious_NoCheckpointFile(t *testing.T) {
	withHome(t, t.TempDir())

	cl := NewChecklist("no-previous", newSteps())
	_, ok := cl.HasPrevious()
	assert.False(t, ok)
}

func TestChecklist_HasPrevious_AllStepsDone(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	// Write a checkpoint where all steps are done.
	cpDir := filepath.Join(home, ".ats", "checkpoints")
	require.NoError(t, os.MkdirAll(cpDir, 0o755))
	cp := Checkpoint{
		Command: "all-done",
		Steps: []ChecklistStep{
			{ID: "step1", Label: "First", Status: Done},
			{ID: "step2", Label: "Second", Status: Skipped},
		},
	}
	data, err := json.Marshal(cp)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cpDir, "all-done.json"), data, 0o644))

	cl := NewChecklist("all-done", []ChecklistStep{
		{ID: "step1", Label: "First", Status: Pending},
		{ID: "step2", Label: "Second", Status: Pending},
	})
	_, ok := cl.HasPrevious()
	assert.False(t, ok, "a fully-complete checkpoint should not offer resume")
}

func TestChecklist_HasPrevious_MalformedCheckpoint(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	cpDir := filepath.Join(home, ".ats", "checkpoints")
	require.NoError(t, os.MkdirAll(cpDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cpDir, "malformed.json"), []byte("{not valid json"), 0o644))

	cl := NewChecklist("malformed", newSteps())
	_, ok := cl.HasPrevious()
	assert.False(t, ok, "malformed checkpoint JSON should not crash; must return false")
}

// --- Cleanup ---

func TestChecklist_Cleanup_RemovesCheckpoint(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	cl := NewChecklist("cleanup-test", newSteps())
	cl.MarkInProgress("step1")

	cpPath := filepath.Join(home, ".ats", "checkpoints", "cleanup-test.json")
	_, err := os.Stat(cpPath)
	require.NoError(t, err)

	cl.Cleanup()

	_, err = os.Stat(cpPath)
	assert.True(t, os.IsNotExist(err), "Cleanup must remove the checkpoint file")
}

func TestChecklist_Cleanup_NoFilePath(t *testing.T) {
	// Constructed without calling withHome, but we still disable color.
	SetNoColor(true)

	cl := &Checklist{
		command:  "no-path",
		steps:    newSteps(),
		filePath: "",
	}
	assert.NotPanics(t, func() { cl.Cleanup() })
}

// --- Render / Summary ---

func TestChecklist_Render_DoesNotPanic(t *testing.T) {
	withHome(t, t.TempDir())
	SetNoColor(true)

	cl := NewChecklist("render-test", newSteps())
	cl.MarkDone("step1")
	cl.MarkFailed("step2", errors.New("boom"))
	cl.MarkSkipped("step3")

	// Render prints styled text to stdout; all we require is that it
	// doesn't panic and exercises the icon switch for every status.
	assert.NotPanics(t, func() { cl.Render() })
}

func TestChecklist_Summary_AllDoneCleansUp(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	cl := NewChecklist("summary-done", newSteps())
	cl.MarkDone("step1")
	cl.MarkDone("step2")
	cl.MarkDone("step3")

	cpPath := filepath.Join(home, ".ats", "checkpoints", "summary-done.json")
	_, err := os.Stat(cpPath)
	require.NoError(t, err, "checkpoint should exist just before Summary")

	cl.Summary()

	_, err = os.Stat(cpPath)
	assert.True(t, os.IsNotExist(err), "Summary with no failures should cleanup")
}

func TestChecklist_Summary_KeepsCheckpointOnFailure(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)

	cl := NewChecklist("summary-fail", newSteps())
	cl.MarkDone("step1")
	cl.MarkFailed("step2", errors.New("boom"))
	cl.MarkSkipped("step3")

	cl.Summary()

	cpPath := filepath.Join(home, ".ats", "checkpoints", "summary-fail.json")
	_, err := os.Stat(cpPath)
	assert.NoError(t, err, "Summary with failures must keep the checkpoint for resume")
}
