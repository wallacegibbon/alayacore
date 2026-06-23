package agent

// RuntimeManager owns the small, writable runtime.conf file that stores
// state which can change while the program is running (currently only
// the active model name). Unlike ModelManager, it is allowed to write
// its file and is used by the session layer to remember the last active
// model across process restarts.
//
// All methods are called from the session's run() goroutine only, so no
// synchronization is needed.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
)

// RuntimeConfig holds runtime configuration that can change during execution
type RuntimeConfig struct {
	ActiveModel string `json:"active_model" config:"active_model"` // Model name (from model.conf)
	ActiveTheme string `json:"active_theme" config:"active_theme"` // Theme name (without .conf extension)
}

// RuntimeManager manages runtime configuration
type RuntimeManager struct {
	config RuntimeConfig
	path   string
}

func NewRuntimeManager(runtimePath string) *RuntimeManager {
	rm := &RuntimeManager{}
	rm.path = runtimePath

	// Load if path is set
	if rm.path != "" {
		_ = rm.Load() //nolint:errcheck // best-effort load on init
	}

	return rm
}

// Load reads the runtime config from file
func (rm *RuntimeManager) Load() error {
	if rm.path == "" {
		return nil
	}

	data, err := os.ReadFile(rm.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create the file with default content
			return rm.save()
		}
		return err
	}

	rm.config = parseRuntimeConfig(string(data))
	return nil
}

// save writes the runtime config to file
func (rm *RuntimeManager) save() error {
	if rm.path == "" {
		return nil
	}

	// Ensure directory exists
	dir := filepath.Dir(rm.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	content := formatRuntimeConfig(rm.config)
	return os.WriteFile(rm.path, []byte(content), 0600)
}

// parseRuntimeConfig parses the key-value runtime config format
func parseRuntimeConfig(content string) RuntimeConfig {
	var cfg RuntimeConfig
	config.ParseKeyValue(content, &cfg)
	return cfg
}

// formatRuntimeConfig formats the runtime config as key-value text
func formatRuntimeConfig(cfg RuntimeConfig) string {
	var sb strings.Builder
	sb.WriteString("# AlayaCore runtime configuration\n")
	sb.WriteString("# This file is automatically updated when you switch models or themes\n")
	sb.WriteString("\n")
	sb.WriteString(config.FormatKeyValue(cfg))
	return sb.String()
}

func (rm *RuntimeManager) GetActiveModel() string {
	return rm.config.ActiveModel
}

// SetActiveModel sets the active model name and saves to file
func (rm *RuntimeManager) SetActiveModel(name string) error {
	rm.config.ActiveModel = name
	return rm.save()
}

func (rm *RuntimeManager) GetActiveTheme() string {
	return rm.config.ActiveTheme
}

// SetActiveTheme sets the active theme name and saves to file
func (rm *RuntimeManager) SetActiveTheme(name string) error {
	rm.config.ActiveTheme = name
	return rm.save()
}
