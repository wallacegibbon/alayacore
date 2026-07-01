package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alayacore/alayacore/internal/theme"
)

func TestThemeSelectorCancelRestoresOriginalTheme(t *testing.T) {
	// Create a theme selector with some themes
	styles := NewStyles(theme.DefaultTheme())
	ts := NewThemeSelector(styles)

	themes := []theme.Info{
		{Name: "theme-dark", Path: "/path/to/theme-dark.conf"},
		{Name: "theme-light", Path: "/path/to/theme-light.conf"},
		{Name: "theme-custom", Path: "/path/to/theme-custom.conf"},
	}

	// Open with "theme-dark" as active theme
	ts.Open(themes, "theme-dark")

	// Verify original theme name is saved
	if ts.GetOriginalThemeName() != "theme-dark" {
		t.Errorf("Expected original theme 'theme-dark', got '%s'", ts.GetOriginalThemeName())
	}

	// Navigate to second theme (theme-light) - simulate j key
	// Note: we pass nil for theme manager since we're just testing selection tracking
	_, handled := ts.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'j'}), nil)
	if !handled {
		t.Log("HandleKeyMsg returned not handled (expected due to nil theme manager)")
	}

	// Verify selection changed
	selected := ts.GetSelectedTheme()
	if selected == nil || selected.Name != "theme-light" {
		t.Errorf("Expected selected theme 'theme-light', got '%v'", selected)
	}

	// Press ESC to cancel
	ts.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}), nil)

	// Verify selector is closed
	if ts.IsOpen() {
		t.Errorf("Expected theme selector to be closed after ESC")
	}

	// Verify original theme name is still available
	if ts.GetOriginalThemeName() != "theme-dark" {
		t.Errorf("Original theme should still be 'theme-dark' after cancel")
	}
}

func TestThemeSelectorEnterSavesTheme(t *testing.T) {
	// Create a theme selector
	styles := NewStyles(theme.DefaultTheme())
	ts := NewThemeSelector(styles)

	themes := []theme.Info{
		{Name: "theme-dark", Path: "/path/to/theme-dark.conf"},
		{Name: "theme-light", Path: "/path/to/theme-light.conf"},
	}

	// Open with "theme-dark" as active theme
	ts.Open(themes, "theme-dark")

	// Navigate to theme-light
	ts.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: 'j'}), nil)

	// Press Enter to select
	ts.HandleKeyMsg(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), nil)

	// Verify theme was selected
	if !ts.ConsumeThemeSelected() {
		t.Errorf("Expected theme to be selected after Enter")
	}

	// Verify selector is closed
	if ts.IsOpen() {
		t.Errorf("Expected theme selector to be closed after Enter")
	}

	// Get the selected theme
	selected := ts.GetSelectedTheme()
	if selected == nil || selected.Name != "theme-light" {
		t.Errorf("Expected selected theme 'theme-light', got '%v'", selected)
	}
}
