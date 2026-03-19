package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestValidateCursor_ClampsOutOfRangeCursor(t *testing.T) {
	// Create a display with a window buffer
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Add some windows
	wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Content 1")
	wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Content 2")
	wb.AppendOrUpdate("window-3", stream.TagTextAssistant, "Content 3")

	// Set cursor to the middle window
	display.SetWindowCursor(1)
	if display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor at 1, got %d", display.GetWindowCursor())
	}

	// Set cursor beyond the range
	display.windowCursor = 10

	// Validate should clamp it
	display.ValidateCursor()

	if display.GetWindowCursor() != 2 {
		t.Errorf("Expected cursor clamped to 2 (last window), got %d", display.GetWindowCursor())
	}
}

func TestValidateCursor_HandlesNegativeCursor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Add some windows
	wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Content 1")
	wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Content 2")

	// Set cursor to invalid negative value
	display.windowCursor = -5

	// Validate should clamp it to -1
	display.ValidateCursor()

	if display.GetWindowCursor() != -1 {
		t.Errorf("Expected cursor clamped to -1, got %d", display.GetWindowCursor())
	}
}

func TestValidateCursor_HandlesEmptyBuffer(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// No windows added, cursor should stay at -1
	display.windowCursor = 5

	display.ValidateCursor()

	// With no windows, cursor should be -1
	if display.GetWindowCursor() != -1 {
		t.Errorf("Expected cursor -1 for empty buffer, got %d", display.GetWindowCursor())
	}
}

func TestValidateCursor_KeepsValidCursor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Add windows
	wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Content 1")
	wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Content 2")
	wb.AppendOrUpdate("window-3", stream.TagTextAssistant, "Content 3")

	// Set cursor to valid position
	display.SetWindowCursor(1)

	// Validate should keep it
	display.ValidateCursor()

	if display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor to stay at 1, got %d", display.GetWindowCursor())
	}
}
