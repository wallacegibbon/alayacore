package terminal

import (
	"encoding/json"
	"testing"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

func TestLastMaxStepsSavedOnlyOnError(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Task in progress
	systemInfoInProgress := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    10,
		CurrentStep: 5,
	}
	data := marshalSystemInfo(t, systemInfoInProgress)
	w.handleSystemTag(string(data))

	// Complete with error — should save
	systemInfoError := agentpkg.SystemInfo{
		InProgress: false,
		MaxSteps:   10,
		TaskError:  true,
	}
	data = marshalSystemInfo(t, systemInfoError)
	w.handleSystemTag(string(data))

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 5 || snap.LastMaxSteps != 10 {
		t.Errorf("Expected last step info (5, 10) on error, got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

func TestLastMaxStepsNotDisplayedOnSuccess(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Task in progress
	systemInfoInProgress := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    10,
		CurrentStep: 7,
	}
	data := marshalSystemInfo(t, systemInfoInProgress)
	w.handleSystemTag(string(data))

	// Complete without error
	systemInfoDone := agentpkg.SystemInfo{
		InProgress: false,
		MaxSteps:   10,
		TaskError:  false,
	}
	data = marshalSystemInfo(t, systemInfoDone)
	w.handleSystemTag(string(data))

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
	systemInfoInProgress := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    10,
		CurrentStep: 5,
	}
	data := marshalSystemInfo(t, systemInfoInProgress)
	w.handleSystemTag(string(data))

	// Complete with error
	systemInfoError := agentpkg.SystemInfo{
		InProgress: false,
		MaxSteps:   10,
		TaskError:  true,
	}
	data = marshalSystemInfo(t, systemInfoError)
	w.handleSystemTag(string(data))

	// New task starts — last step info should be reset
	systemInfoNewTask := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    20,
		CurrentStep: 1,
	}
	data = marshalSystemInfo(t, systemInfoNewTask)
	w.handleSystemTag(string(data))

	snap := w.SnapshotStatus()
	if snap.LastCurrentStep != 0 || snap.LastMaxSteps != 0 {
		t.Errorf("Expected last step info (0, 0) (reset for new task), got (%d, %d)", snap.LastCurrentStep, snap.LastMaxSteps)
	}
}

// Helper function to marshal SystemInfo to JSON
func marshalSystemInfo(t *testing.T, info agentpkg.SystemInfo) []byte {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Failed to marshal SystemInfo: %v", err)
	}
	return data
}
