package terminal

import "time"

// Timing constants for UI responsiveness.
const (
	// ThemePreviewDebounce is the delay before applying a theme preview
	// after a navigation key press. This keeps cursor movement responsive
	// while preventing flicker from rapid navigation.
	ThemePreviewDebounce = 150 * time.Millisecond
)

// Layout constants for window borders and spacing.
const (
	// BorderInnerPadding is the total horizontal padding subtracted from
	// the total width to get the inner content width for bordered windows.
	// This accounts for the left/right border characters + left/right padding.
	BorderInnerPadding = 4
)

// Tab width expansion (standard terminal convention).
const (
	TabWidth = 8
)

// Window tag constants for internal window types in the terminal adapter.
// These are NOT TLV protocol tags (those are defined in internal/stream/stream.go).
// SE = system error window, SN = system notification window.
const (
	TagWindowSE = "SE"
	TagWindowSN = "SN"
)
