package agent

import (
	"os"
	"path/filepath"
)

// resolveConfigPath returns the provided path, or the default ~/.alayacore/<filename>
func resolveConfigPath(providedPath, defaultFilename string) string {
	if providedPath != "" {
		return providedPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".alayacore", defaultFilename)
}