// Package theme defines the color palette (Theme) and loading logic.
// It is shared between adapters (terminal TUI, future GUI) so all
// visual frontends use the same theme data and loading mechanism.
package theme

import (
	_ "embed"
	"fmt"
	"os"
	"sync"

	"github.com/alayacore/alayacore/internal/config"
)

//go:embed dark.conf
var darkThemeContent string

//go:embed light.conf
var lightThemeContent string

//go:embed redpanda.conf
var redpandaThemeContent string

// Theme holds all color values for the UI.
// Each field maps to a key in the .conf theme files.
type Theme struct {
	// Core palette
	Primary   string `config:"primary" json:"primary"`     // Accent color — selected items, focused borders, highlights
	Dim       string `config:"dim" json:"dim"`             // Dimmed color — unfocused borders, muted text
	Muted     string `config:"muted" json:"muted"`         // Muted color — placeholders, secondary labels
	Text      string `config:"text" json:"text"`           // Primary text color
	Warning   string `config:"warning" json:"warning"`     // Warning color — alerts, caution
	Error     string `config:"error" json:"error"`         // Error color — errors, dangerous operations
	Success   string `config:"success" json:"success"`     // Success color — success messages, completion status
	Selection string `config:"selection" json:"selection"` // Selection highlight — selected list item, search match
	Cursor    string `config:"cursor" json:"cursor"`       // Cursor color — text input cursor

	// Diff colors
	Added   string `config:"added" json:"added"`     // Added lines in diff
	Removed string `config:"removed" json:"removed"` // Removed lines in diff

	// Fold indicator character (repeated to form the fold splitter row)
	FoldIndicator string `config:"fold_indicator" json:"fold_indicator"`
}

var (
	defaultThemeOnce sync.Once
	defaultTheme     *Theme
)

// DefaultTheme returns the default theme (Catppuccin Mocha dark).
// Parsed once from the embedded dark.conf and cached. Returns a copy
// so callers (e.g. LoadTheme) can safely modify the result.
func DefaultTheme() *Theme {
	defaultThemeOnce.Do(func() {
		var t Theme
		if warns := config.ParseKeyValue(darkThemeContent, &t); len(warns) > 0 {
			// Embedded default theme should always be valid. If it fails,
			// the binary is corrupted — surface warnings to stderr.
			fmt.Fprintf(os.Stderr, "Warning: default theme parse warnings: %v\n", warns)
		}
		defaultTheme = &t
	})
	cpy := *defaultTheme
	return &cpy
}

// LoadTheme loads a theme from a configuration file.
// Returns the loaded theme or an error if the file cannot be read or parsed.
func LoadTheme(path string) (*Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open theme file: %w", err)
	}

	// Start with defaults, then override with config values
	theme := DefaultTheme()
	if warns := config.ParseKeyValue(string(data), theme); len(warns) > 0 {
		// Surface parse warnings for user-managed theme files, but do not
		// treat them as fatal — unknown fields (e.g. from older versions)
		// should not prevent loading the valid parts of the theme.
		for _, w := range warns {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w.String())
		}
	}
	return theme, nil
}
