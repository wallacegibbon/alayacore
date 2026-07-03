//go:build !windows

package terminal

// defaultEditors is the list of editor binaries to try when $EDITOR is not set.
// Ordered by preference per OS.
var defaultEditors = []string{"vim", "vi"}
