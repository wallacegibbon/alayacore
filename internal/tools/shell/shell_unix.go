//go:build !windows

package shell

// osDefault returns the preferred shell for Unix-like systems.
// bash > zsh > sh, resolved via PATH lookup in detect().
func osDefault() *Shell {
	// On Unix, prefer bash (index 0 in knownShells), then zsh (1), then sh (2).
	// The detect() loop will find the first one that exists on PATH,
	// so we just hint that bash is the preferred default.
	return knownShells[0] // bash
}
