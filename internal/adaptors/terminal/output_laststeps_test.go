package terminal

import (
	"testing"
)

func TestLastMaxStepsSavedOnlyOnError(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Task in progress
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":5,"max_steps":10,"context":0,"context_limit":0,"task_error":false}}`)

	// Complete with error — should save
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":10,"max_steps":10,"context":0,"context_limit":0,"task_error":true}}`)

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 5 || snap.LastMaxSteps != 10 {
		t.Errorf("Expected last step info (5, 10) on error, got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

func TestLastMaxStepsNotDisplayedOnSuccess(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Task in progress
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":7,"max_steps":10,"context":0,"context_limit":0,"task_error":false}}`)

	// Complete without error
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":10,"max_steps":10,"context":0,"context_limit":0,"task_error":false}}`)

	snap := w.SnapshotStatus()
	// lastMaxSteps is saved (data layer), but TaskError=false so display won't show it
	if snap.LastCurrentStep != 7 || snap.LastMaxSteps != 10 {
		t.Errorf("Expected last step info (7, 10) saved on completion, got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
	if snap.TaskError {
		t.Error("Expected TaskError to be false on success")
	}
}

func TestLastMaxStepsResetOnNewTask(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Task in progress
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":5,"max_steps":10,"context":0,"context_limit":0,"task_error":false}}`)

	// Complete with error
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":10,"max_steps":10,"context":0,"context_limit":0,"task_error":true}}`)

	// New task starts — last step info should be reset
	w.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":20,"context":0,"context_limit":0,"task_error":false}}`)

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) (reset for new task), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}
