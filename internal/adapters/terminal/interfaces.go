package terminal

import (
	"io"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// Snapshot Types
// ============================================================================

// ThemeEntry holds a cached theme with its full content, received from the
// session via ThemeListMsg on startup.
type ThemeEntry struct {
	Name  string
	Theme *theme.Theme
}

// StatusSnapshot holds a consistent point-in-time view of session status.
type StatusSnapshot struct {
	ContextTokens   int64
	ContextLimit    int64
	InProgress      bool
	CurrentStep     int
	MaxSteps        int
	LastCurrentStep int
	LastMaxSteps    int
	TaskError       bool
	ReasoningLevel  int
	ActiveTheme     string
	ActiveThemeData *theme.Theme
	VideoFPS        int
	VideoRes        int
}

// ModelSnapshot holds a consistent point-in-time view of model state.
type ModelSnapshot struct {
	Models     []agentpkg.ModelInfo
	ActiveID   int
	ActiveName string
	ConfigPath string
}

// ============================================================================
// Interfaces for Testability
// ============================================================================

// OutputWriter is the interface for writing output from the session.
// It abstracts the terminal output writer for better testability.
type OutputWriter interface {
	io.Writer
	io.Closer

	// Configuration
	SetWindowWidth(width int)
	SetStyles(styles *Styles)

	// Snapshots (replaces many individual getters)
	SnapshotStatus() StatusSnapshot
	SnapshotModels() ModelSnapshot

	// Output methods
	WriteError(format string, args ...any)
	WriteNotify(msg string)

	// Confirm dialog support
	GetPendingToolConfirm() (id, toolName, toolInput string, ok bool)

	// Update signaling
	DrainDirty() bool         // returns true if display was dirty, clears the flag
	DirtyCh() <-chan struct{} // channel-based notification for immediate refresh
	WindowBuffer() *WindowBuffer
}

// Ensure outputWriter implements OutputWriter
var _ OutputWriter = (*outputWriter)(nil)

// DrainDirty returns true if the display was dirty and clears the flag.
// Used by the tick handler for periodic polling.
func (w *outputWriter) DrainDirty() bool {
	return w.dirty.CompareAndSwap(true, false)
}

// DirtyCh returns a channel that fires when the display needs an
// immediate refresh. The Bubble Tea goroutine listens on this channel
// for push-based notifications (faster than waiting for the next tick).
func (w *outputWriter) DirtyCh() <-chan struct{} {
	return w.dirtyCh
}

// WindowBuffer returns the window buffer for direct access
func (w *outputWriter) WindowBuffer() *WindowBuffer {
	return w.windowBuffer
}
