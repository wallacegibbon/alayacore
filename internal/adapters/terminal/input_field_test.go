package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInputFieldInsertion(t *testing.T) {
	f := NewInputField()
	f.SetWidth(20)

	// Simulate real app flow: call Update with tea.KeyPressMsg
	// passed as tea.Msg interface, the way updateFromMsg does it.
	var msg tea.Msg = tea.KeyPressMsg{Text: "h", Code: 'h'}
	f, _ = f.Update(msg)
	if f.Value() != "h" {
		t.Errorf("expected value 'h', got %q", f.Value())
	}
	if f.pos != 1 {
		t.Errorf("expected pos=1, got %d", f.pos)
	}

	msg = tea.KeyPressMsg{Text: "e", Code: 'e'}
	f, _ = f.Update(msg)
	if f.Value() != "he" {
		t.Errorf("expected value 'he', got %q", f.Value())
	}
	if f.pos != 2 {
		t.Errorf("expected pos=2, got %d", f.pos)
	}

	// Type "l", "l", "o"
	msg = tea.KeyPressMsg{Text: "l", Code: 'l'}
	f, _ = f.Update(msg)
	f, _ = f.Update(msg)
	msg = tea.KeyPressMsg{Text: "o", Code: 'o'}
	f, _ = f.Update(msg)

	if f.Value() != "hello" {
		t.Errorf("expected value 'hello', got %q", f.Value())
	}
	if f.pos != 5 {
		t.Errorf("expected pos=5, got %d", f.pos)
	}
}

func TestInputFieldBackspace(t *testing.T) {
	f := NewInputField()
	f.SetWidth(20)

	f, _ = f.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	f, _ = f.Update(tea.KeyPressMsg{Text: "b", Code: 'b'})

	if f.Value() != "ab" || f.pos != 2 {
		t.Fatalf("expected 'ab' pos=2, got %q pos=%d", f.Value(), f.pos)
	}

	f, _ = f.Update(tea.KeyPressMsg{Text: "backspace", Code: tea.KeyBackspace})
	if f.Value() != "a" {
		t.Errorf("expected value 'a', got %q", f.Value())
	}
	if f.pos != 1 {
		t.Errorf("expected pos=1, got %d", f.pos)
	}
}

func TestInputFieldCJKInsertion(t *testing.T) {
	f := NewInputField()
	f.SetWidth(20)

	var msg tea.Msg = tea.KeyPressMsg{Text: "你", Code: '你'}
	f, _ = f.Update(msg)
	if f.Value() != "你" {
		t.Errorf("expected value '你', got %q", f.Value())
	}
	if f.pos != 1 {
		t.Errorf("expected pos=1, got %d", f.pos)
	}

	msg = tea.KeyPressMsg{Text: "好", Code: '好'}
	f, _ = f.Update(msg)
	if f.Value() != "你好" {
		t.Errorf("expected value '你好', got %q", f.Value())
	}
	if f.pos != 2 {
		t.Errorf("expected pos=2, got %d", f.pos)
	}
}

// TestInputFieldViewCursorPosition verifies that View renders the cursor
// at the correct position. This test caught a bug where buildVisibleText
// was returning cursorIdx=0 because the second loop corrupted startIdx.
func TestInputFieldViewCursorPosition(t *testing.T) {
	f := NewInputField()
	f.SetWidth(20)
	f.Focus() // needed to initialize cursorRender

	// Type "hello" through Update calls
	keys := []string{"h", "e", "l", "l", "o"}
	var msg tea.Msg
	for _, k := range keys {
		msg = tea.KeyPressMsg{Text: k, Code: rune(k[0])}
		f, _ = f.Update(msg)
	}

	if f.Value() != "hello" || f.pos != 5 {
		t.Fatalf("setup failed: value=%q pos=%d", f.Value(), f.pos)
	}

	// buildVisibleText should return cursorIdx=5 (not 0!)
	vis, cursorIdx := f.buildVisibleText()
	if cursorIdx != 5 {
		t.Errorf("buildVisibleText cursorIdx=%d, want 5", cursorIdx)
	}
	if string(vis) != "hello" {
		t.Errorf("buildVisibleText visible=%q, want 'hello'", string(vis))
	}

	// View() should render correctly
	_ = f.View()
}
