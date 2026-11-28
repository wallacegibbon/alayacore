package terminal

import (
	"testing"
)

func TestClampWindowCursor_ClampsOutOfRangeCursor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate(tagAssistantText, "window-1", "Content 1")
	wb.AppendOrUpdate(tagAssistantText, "window-2", "Content 2")
	wb.AppendOrUpdate(tagAssistantText, "window-3", "Content 3")

	display = display.WithWindowCursor(1)
	if display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor at 1, got %d", display.GetWindowCursor())
	}

	display.windowCursor = 10

	display = display.ClampWindowCursor()

	if display.GetWindowCursor() != 2 {
		t.Errorf("Expected cursor clamped to 2 (last window), got %d", display.GetWindowCursor())
	}
}

func TestClampWindowCursor_HandlesNegativeCursor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate(tagAssistantText, "window-1", "Content 1")
	wb.AppendOrUpdate(tagAssistantText, "window-2", "Content 2")

	display.windowCursor = -5

	display = display.ClampWindowCursor()

	if display.GetWindowCursor() != -1 {
		t.Errorf("Expected cursor clamped to -1, got %d", display.GetWindowCursor())
	}
}

func TestClampWindowCursor_HandlesEmptyBuffer(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	display.windowCursor = 5

	display = display.ClampWindowCursor()

	if display.GetWindowCursor() != -1 {
		t.Errorf("Expected cursor -1 for empty buffer, got %d", display.GetWindowCursor())
	}
}

func TestClampWindowCursor_KeepsValidCursor(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate(tagAssistantText, "window-1", "Content 1")
	wb.AppendOrUpdate(tagAssistantText, "window-2", "Content 2")
	wb.AppendOrUpdate(tagAssistantText, "window-3", "Content 3")

	display = display.WithWindowCursor(1)

	display = display.ClampWindowCursor()

	if display.GetWindowCursor() != 1 {
		t.Errorf("Expected cursor to stay at 1, got %d", display.GetWindowCursor())
	}
}
