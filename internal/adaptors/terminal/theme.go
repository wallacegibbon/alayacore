package terminal

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Theme holds all color values for the terminal UI
type Theme struct {
	// Core palette
	Base     string // Background color - used for invisible borders
	Surface1 string // Surface color - used for subtle backgrounds
	Accent   string // Primary accent color (blue) - used for focused borders, prompts
	Dim      string // Dimmed color - used for unfocused borders, blurred text
	Muted    string // Muted color - used for placeholder text, system messages
	Text     string // Primary text color (white)
	Warning  string // Warning/accent color (yellow)
	Error    string // Error color (red)
	Success  string // Success color (green)
	Peach    string // Peach color - used for window cursor border highlight
	Cursor   string // Cursor color - used for text input cursor
}

// DefaultTheme returns the default theme (Catppuccin Mocha)
func DefaultTheme() *Theme {
	return &Theme{
		Base:     "#1e1e2e",
		Surface1: "#585b70",
		Accent:   "#89d4fa",
		Dim:      "#45475a",
		Muted:    "#6c7086",
		Text:     "#cdd6f4",
		Warning:  "#f9e2af",
		Error:    "#f38ba8",
		Success:  "#a6e3a1",
		Peach:    "#fab387",
		Cursor:   "#cdd6f4", // Light gray/white for visibility on dark backgrounds
	}
}

// LoadTheme loads a theme from a configuration file
// Returns the loaded theme or an error if the file cannot be read or parsed
func LoadTheme(path string) (*Theme, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open theme file: %w", err)
	}
	defer file.Close()

	theme := DefaultTheme() // Start with defaults
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key: value
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue // Skip malformed lines silently
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Validate color format (must be #hex)
		if !strings.HasPrefix(value, "#") {
			continue // Skip invalid color values
		}

		// Apply to theme
		switch key {
		case "base", "window_border":
			theme.Base = value
		case "surface1":
			theme.Surface1 = value
		case "accent":
			theme.Accent = value
		case "dim":
			theme.Dim = value
		case "muted", "text_muted":
			theme.Muted = value
		case "text":
			theme.Text = value
		case "warning":
			theme.Warning = value
		case "error":
			theme.Error = value
		case "success":
			theme.Success = value
		case "peach":
			theme.Peach = value
		case "cursor":
			theme.Cursor = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading theme file: %w", err)
	}

	return theme, nil
}

// LoadThemeFromPaths tries to load a theme from multiple paths in priority order
// Returns the first successfully loaded theme, or the default theme if none found
func LoadThemeFromPaths(explicitPath string) *Theme {
	// Try explicit path first (highest priority)
	if explicitPath != "" {
		theme, err := LoadTheme(explicitPath)
		if err == nil {
			return theme
		}
		// If explicit path was given but failed, print warning but continue
		fmt.Fprintf(os.Stderr, "Warning: failed to load theme from %s: %v\n", explicitPath, err)
	}

	// Try default user theme path
	homeDir, err := os.UserHomeDir()
	if err == nil {
		defaultPath := filepath.Join(homeDir, ".alayacore", "theme.conf")
		if _, err := os.Stat(defaultPath); err == nil {
			theme, err := LoadTheme(defaultPath)
			if err == nil {
				return theme
			}
			fmt.Fprintf(os.Stderr, "Warning: failed to load theme from %s: %v\n", defaultPath, err)
		}
	}

	// Fallback to default theme
	return DefaultTheme()
}
