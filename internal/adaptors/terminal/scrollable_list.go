package terminal

// ScrollableListCore provides shared state and methods for simple
// scrollable list components (QueueManager, ThemeSelector).
//
// Both components follow the same pattern:
//   - A list of items with keyboard navigation (j/k, up/down)
//   - Arrow keys, Enter, and Escape/q close
//   - Same scroll clamping, border styling, overlay positioning
//
// Unlike FilteredListCore, there is no search/filter input — just
// a plain scrollable list.
//
// Embedding types embed ScrollableListCore and add their own item
// types, rendering, and specialized key handling.
//
// SINGLE-GOROUTINE: All methods of ScrollableListCore are called
// exclusively from the Bubble Tea event loop. No mutex is needed.

import (
	"image/color"
)

// ScrollableListState represents the current state of a scrollable list.
type ScrollableListState int

const (
	ScrollableListClosed ScrollableListState = iota
	ScrollableListOpen
)

// ScrollableListCore holds shared state for scrollable list components.
type ScrollableListCore struct {
	State       ScrollableListState
	SelectedIdx int
	ScrollIdx   int
	Width       int
	Height      int
	Styles      *Styles
	HasFocus    bool
}

// IsOpen returns true if the list is open.
func (sl *ScrollableListCore) IsOpen() bool { return sl.State != ScrollableListClosed }

// Close closes the list.
func (sl *ScrollableListCore) Close() { sl.State = ScrollableListClosed }

// SetStyles updates the styles.
func (sl *ScrollableListCore) SetStyles(styles *Styles) {
	sl.Styles = styles
}

// SetHasFocus sets the application focus state.
// When the app loses focus, all UI elements should be dimmed.
func (sl *ScrollableListCore) SetHasFocus(hasFocus bool) {
	sl.HasFocus = hasFocus
}

// SetSize updates the width and height of the scrollable list.
// Height is clamped to prevent the overlay from exceeding the available space.
func (sl *ScrollableListCore) SetSize(width, height int) {
	if width > 0 {
		sl.Width = width
	}
	sl.Height = min(height-LayoutGap, SelectorMaxHeight)
}

// ClampSelection clamps SelectedIdx and ScrollIdx to valid bounds
// for the given number of items. Resets both to 0 when itemsLen is 0.
func (sl *ScrollableListCore) ClampSelection(itemsLen int) {
	if itemsLen == 0 {
		sl.SelectedIdx = 0
		sl.ScrollIdx = 0
		return
	}
	if sl.SelectedIdx >= itemsLen {
		sl.SelectedIdx = itemsLen - 1
	}
	if sl.SelectedIdx < 0 {
		sl.SelectedIdx = 0
	}
	if sl.ScrollIdx < 0 {
		sl.ScrollIdx = 0
	}
	if sl.ScrollIdx > sl.SelectedIdx {
		sl.ScrollIdx = sl.SelectedIdx
	}
}

// EnsureVisible adjusts ScrollIdx so the selected item is visible.
func (sl *ScrollableListCore) EnsureVisible() {
	listHeight := SelectorListRows
	if sl.SelectedIdx < sl.ScrollIdx {
		sl.ScrollIdx = sl.SelectedIdx
	} else if sl.SelectedIdx >= sl.ScrollIdx+listHeight {
		sl.ScrollIdx = sl.SelectedIdx - listHeight + 1
	}
}

// ListBorderColor returns the border color based on focus state.
func (sl *ScrollableListCore) ListBorderColor() color.Color {
	if !sl.HasFocus {
		return sl.Styles.BorderBlurred
	}
	return sl.Styles.BorderFocused
}

// RenderOverlay renders the list as an overlay on top of base content.
func (sl *ScrollableListCore) RenderOverlay(baseContent, renderedList string, screenWidth, screenHeight int) string {
	if sl.State == ScrollableListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, renderedList, screenWidth, screenHeight)
}
