package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alayacore/alayacore/internal/stream"
)

// TestTerminalResizeCursorValidation tests that the window cursor is properly
// validated when the terminal is resized.
func TestTerminalResizeCursorValidation(t *testing.T) {
	// Create a terminal with initial size
	output := NewTerminalOutput()
	input := stream.NewChanInput(10)
	terminal := NewTerminal(nil, output, input, "", nil, 80, 24)

	// Add some windows to the buffer
	output.windowBuffer.AppendOrUpdate("window-1", stream.TagTextAssistant, "Content 1")
	output.windowBuffer.AppendOrUpdate("window-2", stream.TagTextAssistant, "Content 2")
	output.windowBuffer.AppendOrUpdate("window-3", stream.TagTextAssistant, "Content 3")

	// Set cursor to the middle window
	terminal.display.SetWindowCursor(1)
	if terminal.display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor at 1, got %d", terminal.display.GetWindowCursor())
	}

	// Simulate a resize event
	resizeMsg := tea.WindowSizeMsg{
		Width:  120, // Wider terminal
		Height: 40,  // Taller terminal
	}

	model, _ := terminal.Update(resizeMsg)
	terminal = model.(*Terminal)

	// Cursor should still be valid
	cursor := terminal.display.GetWindowCursor()
	if cursor < 0 || cursor >= 3 {
		t.Errorf("Cursor %d is out of valid range [0, 2] after resize", cursor)
	}
}

// TestTerminalResizeClampsCursor tests that cursor is clamped when windows
// change height during resize.
func TestTerminalResizeClampsCursor(t *testing.T) {
	output := NewTerminalOutput()
	input := stream.NewChanInput(10)
	terminal := NewTerminal(nil, output, input, "", nil, 80, 24)

	// Add windows
	output.windowBuffer.AppendOrUpdate("window-1", stream.TagTextAssistant, "Short")
	output.windowBuffer.AppendOrUpdate("window-2", stream.TagTextAssistant, "Content")

	// Manually set cursor to an invalid value (simulating a bug scenario)
	terminal.display.windowCursor = 10

	// Simulate resize
	resizeMsg := tea.WindowSizeMsg{
		Width:  100,
		Height: 30,
	}

	model, _ := terminal.Update(resizeMsg)
	terminal = model.(*Terminal)

	// Cursor should be clamped to valid range
	cursor := terminal.display.GetWindowCursor()
	if cursor < -1 || cursor >= 2 {
		t.Errorf("Cursor %d should be clamped to [-1, 1] after resize", cursor)
	}

	// Should be clamped to last window (index 1)
	if cursor != 1 {
		t.Errorf("Expected cursor clamped to 1 (last window), got %d", cursor)
	}
}
