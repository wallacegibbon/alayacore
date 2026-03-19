package terminal

import (
	"encoding/json"
	"testing"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

func TestLastMaxStepsPreservation(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Simulate a task in progress with max steps = 10, current step = 5
	systemInfoInProgress := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    10,
		CurrentStep: 5,
	}
	data := marshalSystemInfo(t, systemInfoInProgress)
	w.handleSystemTag(string(data))

	// Verify in-progress state
	if !w.IsInProgress() {
		t.Error("Expected in-progress to be true")
	}
	if w.GetMaxSteps() != 10 {
		t.Errorf("Expected max steps 10, got %d", w.GetMaxSteps())
	}
	lastCurrent, lastMax := w.GetLastStepInfo()
	if lastCurrent != 0 || lastMax != 0 {
		t.Errorf("Expected last step info (0, 0) (not set yet), got (%d, %d)", lastCurrent, lastMax)
	}

	// Simulate task completion (transition from in-progress to done)
	systemInfoCompleted := agentpkg.SystemInfo{
		InProgress:  false,
		MaxSteps:    10,
		CurrentStep: 0,
	}
	data = marshalSystemInfo(t, systemInfoCompleted)
	w.handleSystemTag(string(data))

	// Verify completed state
	if w.IsInProgress() {
		t.Error("Expected in-progress to be false")
	}
	if w.GetMaxSteps() != 10 {
		t.Errorf("Expected max steps 10, got %d", w.GetMaxSteps())
	}
	lastCurrent, lastMax = w.GetLastStepInfo()
	if lastCurrent != 5 || lastMax != 10 {
		t.Errorf("Expected last step info (5, 10) (preserved), got (%d, %d)", lastCurrent, lastMax)
	}

	// Simulate a new task starting with different max steps
	systemInfoNewTask := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    20,
		CurrentStep: 1,
	}
	data = marshalSystemInfo(t, systemInfoNewTask)
	w.handleSystemTag(string(data))

	// Verify new task state - last step info should be reset when new task starts
	if !w.IsInProgress() {
		t.Error("Expected in-progress to be true")
	}
	if w.GetMaxSteps() != 20 {
		t.Errorf("Expected max steps 20, got %d", w.GetMaxSteps())
	}
	lastCurrent, lastMax = w.GetLastStepInfo()
	if lastCurrent != 0 || lastMax != 0 {
		t.Errorf("Expected last step info (0, 0) (reset for new task), got (%d, %d)", lastCurrent, lastMax)
	}
}

func TestLastMaxStepsZeroOnStart(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Initial state - no last step info
	lastCurrent, lastMax := w.GetLastStepInfo()
	if lastCurrent != 0 || lastMax != 0 {
		t.Errorf("Expected last step info (0, 0) initially, got (%d, %d)", lastCurrent, lastMax)
	}

	// First task starts - last step info should still be (0, 0)
	systemInfoFirstTask := agentpkg.SystemInfo{
		InProgress:  true,
		MaxSteps:    5,
		CurrentStep: 1,
	}
	data := marshalSystemInfo(t, systemInfoFirstTask)
	w.handleSystemTag(string(data))

	lastCurrent, lastMax = w.GetLastStepInfo()
	if lastCurrent != 0 || lastMax != 0 {
		t.Errorf("Expected last step info (0, 0) (task not completed yet), got (%d, %d)", lastCurrent, lastMax)
	}
}

func TestLastMaxStepsNotUpdatedWithoutTransition(t *testing.T) {
	w := NewTerminalOutput(DefaultStyles())

	// Send multiple in-progress updates
	for i := 1; i <= 3; i++ {
		systemInfo := agentpkg.SystemInfo{
			InProgress:  true,
			MaxSteps:    15,
			CurrentStep: i,
		}
		data := marshalSystemInfo(t, systemInfo)
		w.handleSystemTag(string(data))
	}

	// Last step info should still be (0, 0) (no completion transition yet)
	lastCurrent, lastMax := w.GetLastStepInfo()
	if lastCurrent != 0 || lastMax != 0 {
		t.Errorf("Expected last step info (0, 0) (no completion), got (%d, %d)", lastCurrent, lastMax)
	}

	// Now complete the task
	systemInfo := agentpkg.SystemInfo{
		InProgress:  false,
		MaxSteps:    15,
		CurrentStep: 0,
	}
	data := marshalSystemInfo(t, systemInfo)
	w.handleSystemTag(string(data))

	// Now last step info should be set to the last current step before completion
	lastCurrent, lastMax = w.GetLastStepInfo()
	if lastCurrent != 3 || lastMax != 15 {
		t.Errorf("Expected last step info (3, 15), got (%d, %d)", lastCurrent, lastMax)
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
