package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestHelpWindowOpenClose(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	if hw.IsOpen() {
		t.Error("Help window should not be open initially")
	}

	hw.Open()
	if !hw.IsOpen() {
		t.Error("Help window should be open after Open()")
	}

	hw.Close()
	if hw.IsOpen() {
		t.Error("Help window should not be open after Close()")
	}
}

func TestHelpWindowNavigation(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw.Open()

	// First selectable item should be index 1 (index 0 is a section header)
	if hw.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1 (first non-header), got %d", hw.selectedIdx)
	}

	// Move down
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'j'}))
	if hw.selectedIdx != 2 {
		t.Errorf("Expected selectedIdx to be 2 after j, got %d", hw.selectedIdx)
	}

	// Move down again
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'j'}))
	if hw.selectedIdx != 3 {
		t.Errorf("Expected selectedIdx to be 3 after second j, got %d", hw.selectedIdx)
	}

	// Move up
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'k'}))
	if hw.selectedIdx != 2 {
		t.Errorf("Expected selectedIdx to be 2 after k, got %d", hw.selectedIdx)
	}
}

func TestHelpWindowSkipsSectionHeaders(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Manually set items with a specific structure for testing
	hw.items = []HelpItem{
		{IsSection: true, Description: "Section 1"},
		{Key: "a", Description: "Action A"},
		{IsSection: true, Description: "Section 2"},
		{Key: "b", Description: "Action B"},
	}
	hw.Open()

	// Should start at index 1 (first non-header)
	if hw.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1, got %d", hw.selectedIdx)
	}

	// Move down should skip header at index 2 and land on index 3
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'j'}))
	if hw.selectedIdx != 3 {
		t.Errorf("Expected selectedIdx to be 3 (skipping header), got %d", hw.selectedIdx)
	}

	// Move up should skip header at index 2 and land on index 1
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'k'}))
	if hw.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1 (skipping header up), got %d", hw.selectedIdx)
	}
}

func TestHelpWindowNavigationBoundary(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	hw.items = []HelpItem{
		{IsSection: true, Description: "Section"},
		{Key: "a", Description: "Action A"},
		{Key: "b", Description: "Action B"},
	}
	hw.Open()

	// Start at index 1
	if hw.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1, got %d", hw.selectedIdx)
	}

	// Move up at top - should stay at 1
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'k'}))
	if hw.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to stay at 1 at top, got %d", hw.selectedIdx)
	}

	// Move down to last item
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'j'}))
	if hw.selectedIdx != 2 {
		t.Errorf("Expected selectedIdx to be 2, got %d", hw.selectedIdx)
	}

	// Move down at bottom - should stay at 2
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'j'}))
	if hw.selectedIdx != 2 {
		t.Errorf("Expected selectedIdx to stay at 2 at bottom, got %d", hw.selectedIdx)
	}
}

func TestHelpWindowCloseKeys(t *testing.T) {
	styles := DefaultStyles()

	// Test 'q' key
	hw := NewHelpWindow(styles)
	hw.Open()
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'q'}))
	if hw.IsOpen() {
		t.Error("Help window should be closed after pressing q")
	}

	// Test 'esc' key
	hw = NewHelpWindow(styles)
	hw.Open()
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if hw.IsOpen() {
		t.Error("Help window should be closed after pressing esc")
	}
}

func TestHelpWindowViewWhenClosed(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// View should return empty when closed
	view := hw.View()
	if view != "" {
		t.Errorf("Expected empty view when closed, got %q", view)
	}

	// RenderOverlay should return baseContent unchanged
	base := "base content"
	overlay := hw.RenderOverlay(base, 80, 24)
	if overlay != base {
		t.Errorf("Expected base content unchanged when closed")
	}
}

func TestHelpWindowViewWhenOpen(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)
	hw.Open()

	view := hw.View()
	if view == "" {
		t.Error("Expected non-empty view when open")
	}

	// Should contain section headers
	if !containsStr(view, "Commands") {
		t.Error("View should contain 'Commands' section")
	}

	// Should contain command entries
	if !containsStr(view, ":continue") {
		t.Error("View should contain ':continue' command")
	}

	// Should contain help text
	if !containsStr(view, "j/k: navigate") {
		t.Error("View should contain navigation help text")
	}
}

func TestHelpWindowSetSize(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	hw.SetSize(80, 30)
	if hw.width != 80 {
		t.Errorf("Expected width 80, got %d", hw.width)
	}
	if hw.height != 30 {
		t.Errorf("Expected height 30, got %d", hw.height)
	}
}

func TestHelpWindowBuildHelpItems(t *testing.T) {
	items := buildHelpItems()

	if len(items) == 0 {
		t.Fatal("Expected non-empty help items")
	}

	// Count sections and items
	sections := 0
	commands := 0
	for _, item := range items {
		if item.IsSection {
			sections++
		} else {
			commands++
		}
	}

	if sections < 2 {
		t.Errorf("Expected at least 2 sections, got %d", sections)
	}
	if commands < 10 {
		t.Errorf("Expected at least 10 command entries, got %d", commands)
	}

	// First item should be a section header
	if !items[0].IsSection {
		t.Error("Expected first item to be a section header")
	}

	// Verify Ctrl+H is in the list
	found := false
	for _, item := range items {
		if item.Key == "Ctrl+H" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected Ctrl+H to be in help items")
	}
}

func TestHelpWindowEnterOnCommand(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Set up items with a :command at index 1
	hw.items = []HelpItem{
		{IsSection: true, Description: "Commands"},
		{Key: ":quit", Description: "Exit application"},
		{Key: ":save", Description: "Save session"},
	}
	hw.Open()
	// Should start at index 1 (first non-header)
	if hw.selectedIdx != 1 {
		t.Fatalf("Expected selectedIdx to be 1, got %d", hw.selectedIdx)
	}

	// Press Enter on :quit
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// Window should be closed
	if hw.IsOpen() {
		t.Error("Help window should be closed after Enter on command")
	}

	// Pending command should be set
	pending := hw.ConsumePendingCommand()
	if pending != ":quit" {
		t.Errorf("Expected pending command ':quit', got %q", pending)
	}

	// Consuming again should return empty
	pending = hw.ConsumePendingCommand()
	if pending != "" {
		t.Errorf("Expected empty after consume, got %q", pending)
	}
}

func TestHelpWindowEnterOnKeyBinding(t *testing.T) {
	styles := DefaultStyles()
	hw := NewHelpWindow(styles)

	// Set up items with a key binding (not a :command)
	hw.items = []HelpItem{
		{IsSection: true, Description: "Global Shortcuts"},
		{Key: "Ctrl+H", Description: "Open help window"},
	}
	hw.Open()

	// Press Enter on Ctrl+H (not a :command)
	hw.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// Window should stay open - Enter on non-command does nothing
	if !hw.IsOpen() {
		t.Error("Help window should stay open after Enter on key binding")
	}

	// No pending command
	pending := hw.ConsumePendingCommand()
	if pending != "" {
		t.Errorf("Expected no pending command, got %q", pending)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
