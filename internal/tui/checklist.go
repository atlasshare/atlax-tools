package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StepStatus represents the state of a checklist step.
type StepStatus string

const (
	Pending    StepStatus = "pending"
	InProgress StepStatus = "in_progress"
	Done       StepStatus = "done"
	Failed     StepStatus = "failed"
	Skipped    StepStatus = "skipped"
)

// ChecklistStep is a single item in the checklist.
type ChecklistStep struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Status StepStatus `json:"status"`
	Error  string     `json:"error,omitempty"`
}

// Checkpoint persists checklist state to disk for resume capability.
type Checkpoint struct {
	Command   string          `json:"command"`
	StartedAt string          `json:"started_at"`
	UpdatedAt string          `json:"updated_at"`
	Steps     []ChecklistStep `json:"steps"`
}

// Checklist manages a visual checklist with persistent checkpointing.
type Checklist struct {
	steps    []ChecklistStep
	command  string
	filePath string
}

// NewChecklist creates a checklist for a command with the given steps.
// It checks for a previous checkpoint and offers resume if found.
func NewChecklist(command string, steps []ChecklistStep) *Checklist {
	cl := &Checklist{
		command: command,
		steps:   steps,
	}

	// Determine checkpoint file path.
	home, err := os.UserHomeDir()
	if err == nil {
		dir := filepath.Join(home, ".ats", "checkpoints")
		_ = os.MkdirAll(dir, 0755)
		cl.filePath = filepath.Join(dir, command+".json")
	}

	return cl
}

// HasPrevious checks if a previous checkpoint exists and returns it.
func (cl *Checklist) HasPrevious() (*Checkpoint, bool) {
	if cl.filePath == "" {
		return nil, false
	}

	data, err := os.ReadFile(cl.filePath)
	if err != nil {
		return nil, false
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, false
	}

	// Check if any steps are not done.
	hasIncomplete := false
	for _, s := range cp.Steps {
		if s.Status != Done && s.Status != Skipped {
			hasIncomplete = true
			break
		}
	}

	if !hasIncomplete {
		return nil, false
	}

	return &cp, true
}

// Resume loads step states from a previous checkpoint.
func (cl *Checklist) Resume(cp *Checkpoint) {
	statusMap := make(map[string]ChecklistStep)
	for _, s := range cp.Steps {
		statusMap[s.ID] = s
	}

	for i := range cl.steps {
		if prev, ok := statusMap[cl.steps[i].ID]; ok {
			if prev.Status == Done || prev.Status == Skipped {
				cl.steps[i].Status = prev.Status
			}
		}
	}
}

// Render prints the full checklist to the terminal.
func (cl *Checklist) Render() {
	fmt.Println()
	for _, s := range cl.steps {
		icon := statusIcon(s.Status)
		label := s.Label
		if s.Status == Failed && s.Error != "" {
			label = fmt.Sprintf("%s (%s)", s.Label, s.Error)
		}
		fmt.Printf("  %s %s\n", icon, label)
	}
	fmt.Println()
}

// MarkInProgress sets a step to in_progress and re-renders.
func (cl *Checklist) MarkInProgress(stepID string) {
	cl.setStatus(stepID, InProgress, "")
	cl.save()
	cl.Render()
}

// MarkDone sets a step to done and re-renders.
func (cl *Checklist) MarkDone(stepID string) {
	cl.setStatus(stepID, Done, "")
	cl.save()
	cl.Render()
}

// MarkFailed sets a step to failed with an error message.
func (cl *Checklist) MarkFailed(stepID string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	cl.setStatus(stepID, Failed, errMsg)
	cl.save()
	cl.Render()
}

// MarkSkipped sets a step to skipped.
func (cl *Checklist) MarkSkipped(stepID string) {
	cl.setStatus(stepID, Skipped, "")
	cl.save()
	cl.Render()
}

// IsComplete returns true if all steps are done or skipped.
func (cl *Checklist) IsComplete() bool {
	for _, s := range cl.steps {
		if s.Status != Done && s.Status != Skipped {
			return false
		}
	}
	return true
}

// IsDone checks if a specific step is already done.
func (cl *Checklist) IsDone(stepID string) bool {
	for _, s := range cl.steps {
		if s.ID == stepID {
			return s.Status == Done
		}
	}
	return false
}

// IsSkipped checks if a specific step is skipped.
func (cl *Checklist) IsSkipped(stepID string) bool {
	for _, s := range cl.steps {
		if s.ID == stepID {
			return s.Status == Skipped
		}
	}
	return false
}

// Cleanup removes the checkpoint file after successful completion.
func (cl *Checklist) Cleanup() {
	if cl.filePath != "" {
		_ = os.Remove(cl.filePath)
	}
}

// Summary prints a final pass/fail/skip count.
func (cl *Checklist) Summary() {
	done, failed, skipped := 0, 0, 0
	for _, s := range cl.steps {
		switch s.Status {
		case Done:
			done++
		case Failed:
			failed++
		case Skipped:
			skipped++
		}
	}

	Divider()
	if failed > 0 {
		Failf("Completed: %d done, %d failed, %d skipped", done, failed, skipped)
		Infof("Resume with: ats %s (checkpoint saved)", cl.command)
	} else {
		Successf("All %d steps completed (%d skipped)", done, skipped)
		cl.Cleanup()
	}
}

// --- internal ---

func (cl *Checklist) setStatus(stepID string, status StepStatus, errMsg string) {
	for i := range cl.steps {
		if cl.steps[i].ID == stepID {
			cl.steps[i].Status = status
			cl.steps[i].Error = errMsg
			return
		}
	}
}

func (cl *Checklist) save() {
	if cl.filePath == "" {
		return
	}

	cp := Checkpoint{
		Command:   cl.command,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Steps:     cl.steps,
	}

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(cl.filePath, data, 0644)
}

func statusIcon(s StepStatus) string {
	switch s {
	case Done:
		return c(BoldGreen, "☑")
	case Failed:
		return c(BoldRed, "☒")
	case InProgress:
		return c(BoldCyan, "◉")
	case Skipped:
		return c(Dim, "⊘")
	default: // Pending
		return c(Dim, "☐")
	}
}
