// Package theme defines the color palette (Theme) and loading logic.
// It is shared between adaptors (terminal TUI, future GUI) so all
// visual frontends use the same theme data and loading mechanism.
package theme

import (
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/config"
)

// Theme holds all color values for the UI.
// Each field maps to a key in the .conf theme files.
type Theme struct {
	// Core palette
	Primary   string `config:"primary" json:"primary"`     // Primary/accent color for highlights and focused borders
	Dim       string `config:"dim" json:"dim"`             // Dimmed color for unfocused borders and blurred text
	Muted     string `config:"muted" json:"muted"`         // Muted color for placeholder and secondary text
	Text      string `config:"text" json:"text"`           // Primary text color
	Warning   string `config:"warning" json:"warning"`     // Warning color (yellow/orange)
	Error     string `config:"error" json:"error"`         // Error color (red)
	Success   string `config:"success" json:"success"`     // Success color (green)
	Selection string `config:"selection" json:"selection"` // Selection/cursor border highlight color
	Cursor    string `config:"cursor" json:"cursor"`       // Text input cursor color

	// Diff colors
	Added   string `config:"added" json:"added"`     // Added lines in diff (green)
	Removed string `config:"removed" json:"removed"` // Removed lines in diff (red)

	// Fold indicator character (repeated to form the fold splitter row)
	FoldIndicator string `config:"fold_indicator" json:"fold_indicator"`
}

// DefaultTheme returns the default theme (Catppuccin Mocha)
func DefaultTheme() *Theme {
	return &Theme{
		Primary:   "#89d4fa",
		Dim:       "#313244",
		Muted:     "#6c7086",
		Text:      "#cdd6f4",
		Warning:   "#f9e2af",
		Error:     "#f38ba8",
		Success:   "#a6e3a1",
		Selection: "#fab387",
		Cursor:    "#cdd6f4",
		Added:     "#a6e3a1",
		Removed:   "#f38ba8",

		FoldIndicator: "⁝",
	}
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
	config.ParseKeyValue(string(data), theme)
	return theme, nil
}
