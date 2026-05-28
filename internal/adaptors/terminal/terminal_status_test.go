package terminal

import (
	"fmt"
	"testing"
)

func TestStatusBarShowsLastMaxStepsOnError(t *testing.T) {
	// Create output writer and simulate task ending with error
	out := NewTerminalOutput(DefaultStyles())

	// Simulate task in progress with max steps = 50, current step = 2
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":2,"max_steps":50,"context":0,"context_limit":0,"task_error":false,"queue_items":0}}`)

	// Simulate task ending with error
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":false,"current_step":50,"max_steps":50,"context":0,"context_limit":0,"task_error":true,"queue_items":0}}`)

	// Create terminal with the output writer
	styles := DefaultStyles()
	terminal := &Terminal{
		out:           out,
		display:       NewDisplayModel(out.WindowBuffer(), styles),
		input:         NewInputModel(styles),
		editor:        NewEditor(),
		windowWidth:   80,
		windowHeight:  24,
		styles:        styles,
		focusedWindow: "input",
		hasFocus:      true,
	}

	// Update status
	terminal.updateStatus()

	// Check that status shows last step info (2/50) after completion
	expectedSubstring := "2/50"
	plain := stripANSI(terminal.statusText)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

func TestStatusBarShowsCurrentStepsDuringProgress(t *testing.T) {
	// Create output writer and simulate task in progress
	out := NewTerminalOutput(DefaultStyles())

	// Simulate task in progress
	out.handleSystemMsg(`{"type":"task","data":{"in_progress":true,"current_step":7,"max_steps":20,"context":0,"context_limit":0,"task_error":false,"queue_items":0}}`)

	// Create terminal with the output writer
	styles := DefaultStyles()
	terminal := &Terminal{
		out:           out,
		display:       NewDisplayModel(out.WindowBuffer(), styles),
		input:         NewInputModel(styles),
		editor:        NewEditor(),
		windowWidth:   80,
		windowHeight:  24,
		styles:        styles,
		focusedWindow: "input",
		hasFocus:      true,
	}

	// Update status
	terminal.updateStatus()

	// Check that status shows current step progress
	expectedSubstring := "7/20"
	plain := stripANSI(terminal.statusText)
	if !containsSubstring(plain, expectedSubstring) {
		t.Errorf("Expected status to contain %q, got %q", expectedSubstring, plain)
	}
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{9999, "10.0K"},
		{10000, "10K"},
		{15500, "15.5K"},
		{100000, "100K"},
		{999499, "999.5K"},
		{1000000, "1M"},
		{1500000, "1.5M"},
		{10000000, "10M"},
		{128000, "128K"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := formatTokenCount(tt.input)
			if got != tt.expected {
				t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
