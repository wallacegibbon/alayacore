package terminal

import "time"

// Layout constants
const (
	DefaultWidth  = 80
	DefaultHeight = 20

	// Row allocation: input box, status bar, newlines
	InputRows  = 3
	StatusRows = 1
	LayoutGap  = 4 // input + status + newlines between sections
)

// Timing constants
const (
	UpdateThrottleInterval = 100 * time.Millisecond // batch rapid display updates (lower = sooner signal)
	TickInterval           = 250 * time.Millisecond // polling during streaming (lower = smoother refresh)
	FlusherInterval        = 50 * time.Millisecond  // update flusher tick
	SubmitTickDelay        = 50 * time.Millisecond  // delay before first tick after submit
)
