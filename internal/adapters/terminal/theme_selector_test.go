package terminal

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alayacore/alayacore/internal/theme"
)

func TestThemeSelectorCancelRestoresOriginalTheme(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	ts := NewThemeSelector(styles)

	themes := []theme.Info{
		{Name: "theme-dark", Path: "/path/to/theme-dark.conf"},
		{Name: "theme-light", Path: "/path/to/theme-light.conf"},
		{Name: "theme-custom", Path: "/path/to/theme-custom.conf"},
	}

	ts = ts.Open(themes, "theme-dark")

	if ts.GetOriginalThemeName() != "theme-dark" {
		t.Errorf("Expected original theme 'theme-dark', got '%s'", ts.GetOriginalThemeName())
	}

	ts, result := ts.Update(tea.KeyPressMsg(tea.Key{Code: 'j'}), nil)

	selected := ts.GetSelectedTheme()
	if selected == nil || selected.Name != "theme-light" {
		t.Errorf("Expected selected theme 'theme-light', got '%v'", selected)
	}

	ts, result = ts.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}), nil)

	if !result.Closed {
		t.Errorf("Expected ESC to close the selector")
	}
	if ts.IsOpen() {
		t.Errorf("Expected theme selector to be closed after ESC")
	}
	if ts.GetOriginalThemeName() != "theme-dark" {
		t.Errorf("Original theme should still be 'theme-dark' after cancel, got '%s'", ts.GetOriginalThemeName())
	}
}

func TestThemeSelectorEnterSavesTheme(t *testing.T) {
	styles := NewStyles(theme.DefaultTheme())
	ts := NewThemeSelector(styles)

	themes := []theme.Info{
		{Name: "theme-dark", Path: "/path/to/theme-dark.conf"},
		{Name: "theme-light", Path: "/path/to/theme-light.conf"},
	}

	ts = ts.Open(themes, "theme-dark")

	ts, _ = ts.Update(tea.KeyPressMsg(tea.Key{Code: 'j'}), nil)

	ts, result := ts.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), nil)

	if !result.ThemeSelected {
		t.Errorf("Expected theme to be selected after Enter")
	}
	if ts.IsOpen() {
		t.Errorf("Expected theme selector to be closed after Enter")
	}
	selected := ts.GetSelectedTheme()
	if selected == nil || selected.Name != "theme-light" {
		t.Errorf("Expected selected theme 'theme-light', got '%v'", selected)
	}
}
