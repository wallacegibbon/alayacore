package terminal

import "time"

// ============================================================================
// Layout Constants
// ============================================================================

const (
	DefaultWidth  = 80
	DefaultHeight = 20

	// Row allocation: input box, status bar, newlines
	InputRows  = 3
	StatusRows = 1
	LayoutGap  = 4 // input + status + newlines between sections

	// Component sizing
	InputPaddingH     = 8  // horizontal padding for input fields (border + padding both sides)
	SelectorMaxHeight = 30 // maximum height for model selector and similar overlays
)

// ============================================================================
// Timing Constants
// ============================================================================

const (
	UpdateThrottleInterval = 100 * time.Millisecond // batch rapid display updates (lower = sooner signal)
	TickInterval           = 250 * time.Millisecond // polling during streaming (lower = smoother refresh)
	FlusherInterval        = 50 * time.Millisecond  // update flusher tick
	SubmitTickDelay        = 50 * time.Millisecond  // delay before first tick after submit
)
