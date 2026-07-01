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
	darkTheme := `primary: #89d4fa
dim: #313244
muted: #6c7086
text: #cdd6f4
warning: #f9e2af
error: #f38ba8
success: #a6e3a1
selection: #fab387
cursor: #cdd6f4
added: #a6e3a1
removed: #f38ba8
fold_indicator: "⁝"
`
	darkPath := filepath.Join(tm.themesFolder, "theme-dark.conf")
	_ = os.WriteFile(darkPath, []byte(darkTheme), 0600) // best-effort default creation

	lightTheme := `primary: #1e66f5
dim: #d0d0d8
muted: #6c6f85
text: #4c4f69
warning: #df8e1d
error: #d20f39
success: #40a02b
selection: #881337
cursor: #1e1e2e
added: #40a02b
removed: #d20f39
fold_indicator: "⁝"
`
	lightPath := filepath.Join(tm.themesFolder, "theme-light.conf")
	_ = os.WriteFile(lightPath, []byte(lightTheme), 0600) // best-effort default creation

	redpandaTheme := `primary: #e24328
dim: #2c1c18
muted: #6b4e44
text: #f0e6e0
warning: #f77923
error: #ea4a3e
success: #48bb78
selection: #943d28
cursor: #e24328
added: #68d391
removed: #f9944f
fold_indicator: "⁝"
`
	redpandaPath := filepath.Join(tm.themesFolder, "theme-redpanda.conf")
	_ = os.WriteFile(redpandaPath, []byte(redpandaTheme), 0600) // best-effort default creation
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
