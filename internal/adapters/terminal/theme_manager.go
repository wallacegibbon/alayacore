package terminal

// ThemeManager is a thin wrapper around theme.Manager that adds
// terminal-specific init error collection (displayed at startup).
//
// The core loading, listing, and default-creation logic lives in
// internal/theme so it can be shared with future GUI adapters.

import (
	"github.com/alayacore/alayacore/internal/theme"
)

// ThemeManager wraps theme.Manager with terminal-specific init error collection.
type ThemeManager struct {
	inner *theme.Manager
	ec    *InitErrorCollector
}

// NewThemeManager creates a new theme manager with init error collection.
func NewThemeManager(themesFolder string) *ThemeManager {
	ec := &InitErrorCollector{}
	inner := theme.NewManager(themesFolder)
	return &ThemeManager{inner: inner, ec: ec}
}

// GetInitErrors returns all collected init errors and clears the buffer.
func (tm *ThemeManager) GetInitErrors() []InitError {
	// Merge theme.Manager parse errors into the init error collector.
	for _, e := range tm.inner.GetLoadErrors() {
		tm.ec.Addf("%s", e)
	}
	return tm.ec.GetAndClear()
}
