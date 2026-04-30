package terminal

import (
	"io"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// ============================================================================
// Snapshot Types
// ============================================================================

// StatusSnapshot holds a consistent point-in-time view of session status.
type StatusSnapshot struct {
	ContextTokens   int64
	ContextLimit    int64
	QueueCount      int
	InProgress      bool
	CurrentStep     int
	MaxSteps        int
	LastCurrentStep int
	LastMaxSteps    int
	TaskError       bool
	ThinkLevel      int
}

// ModelSnapshot holds a consistent point-in-time view of model state.
type ModelSnapshot struct {
	Models     []agentpkg.ModelInfo
	ActiveID   int
	ActiveName string
	HasModels  bool
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

	// stream.Output methods
	WriteString(s string) (n int, err error)
	Flush() error

	// Configuration
	SetWindowWidth(width int)
	SetStyles(styles *Styles)

	// Snapshots (replaces many individual getters)
	SnapshotStatus() StatusSnapshot
	SnapshotModels() ModelSnapshot

	// Queue management
	GetQueueItems() []QueueItem

	// Output methods
	AppendError(format string, args ...any)
	WriteNotify(msg string)

	// Update signaling
	DrainDirty() bool // returns true if display was dirty, clears the flag
	WindowBuffer() *WindowBuffer
}

// Ensure outputWriter implements OutputWriter
var _ OutputWriter = (*outputWriter)(nil)

// DrainDirty returns true if the display was dirty and clears the flag.
// This replaces the channel-based update signaling with a simple atomic bool.
func (w *outputWriter) DrainDirty() bool {
	return w.dirty.CompareAndSwap(true, false)
}

// WindowBuffer returns the window buffer for direct access
func (w *outputWriter) WindowBuffer() *WindowBuffer {
	return w.windowBuffer
}
