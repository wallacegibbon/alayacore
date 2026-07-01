package terminal

// ThemeManager is a thin wrapper around theme.Manager that adds
// terminal-specific initialization warnings (displayed at startup).
//
// The core loading, listing, and default-creation logic lives in
// internal/theme so it can be shared with future GUI adapters.

import (
	"github.com/alayacore/alayacore/internal/theme"
)

// ThemeManager wraps theme.Manager with terminal-specific warnings.
type ThemeManager struct {
	inner *theme.Manager
	wc    *WarningCollector
}

// NewThemeManager creates a new theme manager with warning collection.
func NewThemeManager(themesFolder string) *ThemeManager {
	wc := &WarningCollector{}
	inner := theme.NewManager(themesFolder)
	return &ThemeManager{inner: inner, wc: wc}
}

// ReloadThemes reloads the list of available themes.
func (tm *ThemeManager) ReloadThemes() {
	tm.inner.ReloadThemes()
}

func (tm *ThemeManager) GetThemes() []theme.Info {
	return tm.inner.GetThemes()
}

func (tm *ThemeManager) GetThemesFolder() string {
	return tm.inner.GetThemesFolder()
}

// LoadTheme loads a theme by name, falling back to default on failure.
func (tm *ThemeManager) LoadTheme(name string) *theme.Theme {
	return tm.inner.LoadTheme(name)
}

// ThemeExists checks if a theme with the given name exists.
func (tm *ThemeManager) ThemeExists(name string) bool {
	return tm.inner.ThemeExists(name)
}

// GetWarnings returns all collected warnings and clears the buffer.
func (tm *ThemeManager) GetWarnings() []Warning {
	return tm.wc.GetAndClear()
}
