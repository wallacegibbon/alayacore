package terminal

// ScrollableListCore provides shared state, methods, and key handling
// for simple scrollable list components (ModelSelector, ThemeSelector).
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
// types, rendering, and specialized key handling via the onExtra callback.
//
// SINGLE-GOROUTINE: All methods of ScrollableListCore are called
// exclusively from the Bubble Tea event loop. No mutex is needed.

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
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

func (sl ScrollableListCore) IsOpen() bool { return sl.State != ScrollableListClosed }

// Close closes the list.
func (sl ScrollableListCore) Close() ScrollableListCore { sl.State = ScrollableListClosed; return sl }

// SetStyles updates the styles.
func (sl ScrollableListCore) SetStyles(styles *Styles) ScrollableListCore {
	sl.Styles = styles
	return sl
}

// When the app loses focus, all UI elements should be dimmed.
func (sl ScrollableListCore) SetHasFocus(hasFocus bool) ScrollableListCore {
	sl.HasFocus = hasFocus
	return sl
}

// SetSize updates the width and height of the scrollable list.
// Height is clamped to prevent the overlay from exceeding the available space.
func (sl ScrollableListCore) SetSize(width, height int) ScrollableListCore {
	if width > 0 {
		sl.Width = width
	}
	sl.Height = min(height-LayoutGap, SelectorMaxHeight)
	return sl
}

// ClampSelection clamps SelectedIdx and ScrollIdx to valid bounds
// for the given number of items. Resets both to 0 when itemsLen is 0.
func (sl ScrollableListCore) ClampSelection(itemsLen int) ScrollableListCore {
	if itemsLen == 0 {
		sl.SelectedIdx = 0
		sl.ScrollIdx = 0
		return sl
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
	return sl
}

// EnsureVisible adjusts ScrollIdx so the selected item is visible.
func (sl ScrollableListCore) EnsureVisible() ScrollableListCore {
	listHeight := SelectorListRows
	if sl.SelectedIdx < sl.ScrollIdx {
		sl.ScrollIdx = sl.SelectedIdx
	} else if sl.SelectedIdx >= sl.ScrollIdx+listHeight {
		sl.ScrollIdx = sl.SelectedIdx - listHeight + 1
	}
	return sl
}

// ListBorderColor returns the border color based on focus state.
func (sl ScrollableListCore) ListBorderColor() color.Color {
	if !sl.HasFocus {
		return sl.Styles.BorderBlurred
	}
	return sl.Styles.BorderFocused
}

// RenderOverlay renders the list as an overlay on top of base content.
func (sl ScrollableListCore) RenderOverlay(baseContent, renderedList string, screenWidth, screenHeight int) string {
	if sl.State == ScrollableListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, renderedList, screenWidth, screenHeight)
}

// HandleKeyMsg handles common scrollable list navigation keys.
// Returns (handled, isClose). If handled is false, the caller should
// check component-specific keys. If isClose is true, the list was closed.
// itemsLen is the number of items in the list (for bounds checking).
func (sl ScrollableListCore) HandleKeyMsg(msg tea.KeyMsg, itemsLen int) (ScrollableListCore, bool, bool) {
	switch msg.String() {
	case keyQ, keyEsc:
		sl.State = ScrollableListClosed
		return sl, true, true
	case keyJ, keyDown:
		if sl.SelectedIdx < itemsLen-1 {
			sl.SelectedIdx++
			sl = sl.EnsureVisible()
		}
		return sl, true, false
	case keyK, keyUp:
		if sl.SelectedIdx > 0 {
			sl.SelectedIdx--
			sl = sl.EnsureVisible()
		}
		return sl, true, false
	}
	return sl, false, false
}
