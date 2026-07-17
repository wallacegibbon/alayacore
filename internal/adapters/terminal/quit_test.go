package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alayacore/alayacore/internal/theme"
)

func TestQuitCommandRequiresConfirm(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

	// Should return a model and no command (just shows dialog)
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd != nil {
		t.Fatal(":q should not emit command immediately, should show confirm dialog")
	}

	// Quit confirmation dialog should be shown
	if !terminal.overlays.ConfirmOverlay().IsOpen() {
		t.Fatal(":q should open confirm overlay")
	}
	if terminal.overlays.ConfirmOverlay().Kind() != ConfirmQuit {
		t.Errorf(":q should set confirm overlay kind to ConfirmQuit, got %v", terminal.overlays.ConfirmOverlay().Kind())
	}

	// confirmFromCommand should be set to true
	if !terminal.confirmFromCommand {
		t.Error(":q should set confirmFromCommand to true")
	}
}

func TestQuitCommandCanceledClearsInput(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	model, _ := terminal.Update(msg); terminal = model.(Terminal)

	// Verify dialog is shown
	if !terminal.overlays.ConfirmOverlay().IsOpen() {
		t.Fatalf("Expected confirm overlay to be open")
	}
	if terminal.overlays.ConfirmOverlay().Kind() != ConfirmQuit {
		t.Fatalf("Expected ConfirmQuit dialog, got %v", terminal.overlays.ConfirmOverlay().Kind())
	}

	// Press 'n' to cancel
	msg = tea.KeyPressMsg(tea.Key{Code: 'n'})
	model, _ = terminal.Update(msg); terminal = model.(Terminal)

	// Input should be cleared after canceling the dialog
	if terminal.input.Value() != "" {
		t.Errorf("Input should be cleared after canceling :q confirmation, got %q", terminal.input.Value())
	}

	// Dialog should be closed
	if terminal.overlays.ConfirmOverlay().IsOpen() {
		t.Errorf("Dialog should be closed after canceling")
	}
}

func TestQuitCommandEscapeCancels(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	model, _ := terminal.Update(msg); terminal = model.(Terminal)

	// Verify dialog is shown
	if !terminal.overlays.ConfirmOverlay().IsOpen() {
		t.Fatalf("Expected confirm overlay to be open")
	}
	if terminal.overlays.ConfirmOverlay().Kind() != ConfirmQuit {
		t.Fatalf("Expected ConfirmQuit dialog, got %v", terminal.overlays.ConfirmOverlay().Kind())
	}

	// Press Escape to cancel
	msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	model, _ = terminal.Update(msg); terminal = model.(Terminal)

	// Input should be cleared after canceling the dialog
	if terminal.input.Value() != "" {
		t.Errorf("Input should be cleared after canceling :q confirmation with Escape, got %q", terminal.input.Value())
	}

	// Dialog should be closed
	if terminal.overlays.ConfirmOverlay().IsOpen() {
		t.Errorf("Dialog should be closed after canceling")
	}
}

func TestQuitCommandConfirmed(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.SetValue(":q")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	model, _ := terminal.Update(msg); terminal = model.(Terminal)

	// Verify dialog is shown
	if !terminal.overlays.ConfirmOverlay().IsOpen() {
		t.Fatalf("Expected confirm overlay to be open")
	}
	if terminal.overlays.ConfirmOverlay().Kind() != ConfirmQuit {
		t.Fatalf("Expected ConfirmQuit dialog, got %v", terminal.overlays.ConfirmOverlay().Kind())
	}

	// Press 'y' to confirm
	msg = tea.KeyPressMsg(tea.Key{Code: 'y'})
	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

	// Should return quit command
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd == nil {
		t.Fatal("Pressing 'y' should return quit command")
	}

	// Should be quitting
	if !terminal.quitting {
		t.Error("Terminal should be in quitting state after confirming :q")
	}
}

func TestFullQuitCommandRequiresConfirm(t *testing.T) {
	terminal := NewTerminalWithTheme(NewTerminalOutput(DefaultStyles()), nopWriteCloser{}, nil, 80, 24, theme.DefaultTheme(), nil, "theme-dark")
	terminal.input = terminal.input.SetValue(":quit")

	// Press Enter to submit the command
	terminal.focusInput()
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})

	model, cmd := terminal.Update(msg)
	terminal = model.(Terminal)

	// Should return a model and no command (just shows dialog)
	if model == nil {
		t.Fatal("Update returned nil model")
	}

	if cmd != nil {
		t.Fatal(":quit should not emit command immediately, should show confirm dialog")
	}

	// Quit confirmation dialog should be shown
	if !terminal.overlays.ConfirmOverlay().IsOpen() {
		t.Fatal(":quit should open confirm overlay")
	}
	if terminal.overlays.ConfirmOverlay().Kind() != ConfirmQuit {
		t.Errorf(":quit should set confirm overlay kind to ConfirmQuit, got %v", terminal.overlays.ConfirmOverlay().Kind())
	}

	// confirmFromCommand should be set to true
	if !terminal.confirmFromCommand {
		t.Error(":quit should set confirmFromCommand to true")
	}
}
