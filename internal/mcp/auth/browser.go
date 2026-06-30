package auth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenURL opens the given URL in the system's default web browser.
// Supports Linux (xdg-open), macOS (open), and Windows (rundll32).
func OpenURL(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux and others
		cmd = "xdg-open"
		args = []string{url}
	}

	if err := exec.Command(cmd, args...).Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}
