package theme

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Info represents a theme's metadata.
type Info struct {
	Name string // Theme name (filename without .conf extension)
	Path string // Full path to the theme file
}

// Manager handles theme loading and management.
// It scans a themes folder for .conf files, provides lookup by name,
// and creates default themes on first run.
type Manager struct {
	themesFolder string
	themes       []Info
}

// NewManager creates a new theme manager.
// themesFolder is the directory containing *.conf theme files.
// If it's empty, theme listing is disabled.
// If the directory doesn't exist, it's created with default themes.
func NewManager(themesFolder string) *Manager {
	tm := &Manager{themesFolder: themesFolder}
	tm.initializeThemesFolder()
	tm.ReloadThemes()
	return tm
}

// initializeThemesFolder creates the themes folder and populates it with default themes.
func (tm *Manager) initializeThemesFolder() {
	if tm.themesFolder == "" {
		return
	}
	if _, err := os.Stat(tm.themesFolder); os.IsNotExist(err) {
		if err := os.MkdirAll(tm.themesFolder, 0755); err != nil {
			return
		}
		tm.createDefaultThemes()
	}
}

// createDefaultThemes creates the default theme-dark.conf and theme-light.conf files.
func (tm *Manager) createDefaultThemes() {
	darkTheme := `# AlayaCore Dark Theme
# Based on Catppuccin Mocha color palette

# Primary - accent color for highlights and focused borders
primary: #89d4fa

# Dim - for unfocused borders and blurred text
dim: #313244

# Muted - for placeholder and secondary text
muted: #6c7086

# Text - primary text color
text: #cdd6f4

# Warning - for warnings (yellow/orange)
warning: #f9e2af

# Error - for errors (red)
error: #f38ba8

# Success - for success indicators (green)
success: #a6e3a1

# Selection - for cursor border highlight
selection: #fab387

# Cursor - text input cursor color
cursor: #cdd6f4

# Diff colors
# Added lines (green)
added: #a6e3a1

# Removed lines (red)
removed: #f38ba8

# Fold indicator character
fold_indicator: "⁝"
`
	darkPath := filepath.Join(tm.themesFolder, "theme-dark.conf")
	_ = os.WriteFile(darkPath, []byte(darkTheme), 0600) //nolint:errcheck // best-effort default creation

	lightTheme := `# AlayaCore Light Theme
# Based on Catppuccin Latte color palette
# Optimized for white/light terminal backgrounds

# Primary - deep blue for visibility on light backgrounds
primary: #1e66f5

# Dim - for unfocused borders (darker, closer to background)
dim: #d0d0d8

# Muted - for placeholder and secondary text
muted: #6c6f85

# Text - dark for readability
text: #4c4f69

# Warning - orange for visibility
warning: #df8e1d

# Error - deep red for errors
error: #d20f39

# Success - deep green for success indicators
success: #40a02b

# Selection - dark maroon for cursor border highlight
selection: #881337

# Cursor - dark color for visibility
cursor: #1e1e2e

# Diff colors
# Added lines (deep green)
added: #40a02b

# Removed lines (deep red)
removed: #d20f39

# Fold indicator character
fold_indicator: "⁝"
`
	lightPath := filepath.Join(tm.themesFolder, "theme-light.conf")
	_ = os.WriteFile(lightPath, []byte(lightTheme), 0600) //nolint:errcheck // best-effort default creation

	redpandaTheme := `# AlayaCore Redpanda Dark Theme
# Warm reddish-brown palette with red/orange dominant colors,
# inspired by the Redpanda brand palette.

# Primary - brand red-orange for highlights and focused borders
primary: #e24328

# Dim - dark warm brown for unfocused borders
dim: #2c1c18

# Muted - warm brown for placeholder and secondary text
muted: #6b4e44

# Text - warm off-white for comfortable reading
text: #f0e6e0

# Warning - warm orange
warning: #f77923

# Error - bright red-orange
error: #ea4a3e

# Success - muted green
success: #48bb78

# Selection - warm dark red-brown for cursor border highlight
selection: #943d28

# Cursor - brand red-orange
cursor: #e24328

# Diff colors
# Added lines (green)
added: #68d391

# Removed lines (bright red-orange)
removed: #f9944f

# Fold indicator character
fold_indicator: "⁝"
`
	redpandaPath := filepath.Join(tm.themesFolder, "theme-redpanda.conf")
	_ = os.WriteFile(redpandaPath, []byte(redpandaTheme), 0600) //nolint:errcheck // best-effort default creation
}

// ReloadThemes reloads the list of available themes from the themes folder.
func (tm *Manager) ReloadThemes() {
	tm.themes = nil
	if tm.themesFolder == "" {
		return
	}

	entries, err := os.ReadDir(tm.themesFolder)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".conf") {
			continue
		}
		themeName := strings.TrimSuffix(name, ".conf")
		tm.themes = append(tm.themes, Info{
			Name: themeName,
			Path: filepath.Join(tm.themesFolder, name),
		})
	}

	sort.Slice(tm.themes, func(i, j int) bool {
		return tm.themes[i].Name < tm.themes[j].Name
	})
}

func (tm *Manager) GetThemes() []Info {
	if tm.themes == nil {
		return nil
	}
	result := make([]Info, len(tm.themes))
	copy(result, tm.themes)
	return result
}

func (tm *Manager) GetThemesFolder() string {
	return tm.themesFolder
}

// LoadTheme loads a theme by name.
// If the theme doesn't exist or name is empty, returns the default theme.
func (tm *Manager) LoadTheme(name string) *Theme {
	if name == "" {
		return DefaultTheme()
	}
	for _, t := range tm.themes {
		if t.Name == name {
			loaded, err := LoadTheme(t.Path)
			if err != nil {
				return DefaultTheme()
			}
			return loaded
		}
	}
	return DefaultTheme()
}

// ThemeExists checks if a theme with the given name exists.
func (tm *Manager) ThemeExists(name string) bool {
	for _, t := range tm.themes {
		if t.Name == name {
			return true
		}
	}
	return false
}
