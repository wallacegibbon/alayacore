package terminal

import (
	"io"

	"github.com/alayacore/alayacore/internal/config"
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

	// MCP init status — reflects the current phase of MCP initialization.
	// Values: "" (no MCP or not started), "starting", "ready", "auth_required".
	MCPInitStatus string

	// MCP auth status — session-driven OAuth overlay state.
	// MCPAuthStatus values:
	//   ""        — no active OAuth overlay
	//   "confirm" — session wants a y/n confirm dialog for MCPAuthServer
	//   "in_progress" — OAuth flow is running for MCPAuthServer
	//   "done"   — all OAuth servers processed, close overlay
	MCPAuthStatus    string
	MCPAuthServer    string // server currently being prompted/authorized
	MCPAuthServerURL string // URL for the confirm dialog
}

// ModelSnapshot holds a consistent point-in-time view of model state.
type ModelSnapshot struct {
	Models     []config.ModelConfig
	ActiveID   int
	ActiveName string
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
