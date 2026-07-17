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
// types, rendering, and specialized key handling.
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

// ScrollableListUpdate describes the result of a ScrollableListCore update.
type ScrollableListUpdate struct {
	Handled bool
	IsClose bool // true if the list was closed by this key
}

// ScrollableListCore holds shared state for scrollable list components.
type ScrollableListCore struct {
	State       ScrollableListState
	SelectedIdx int
	ScrollIdx   int
	Width       int
	Height      int
	Styles      *Styles
	HasFocus    bool
	ItemsLen    int // number of items in the parent's list (for bounds checking)
}

func (sl ScrollableListCore) IsOpen() bool { return sl.State != ScrollableListClosed }

// Close closes the list.
func (sl ScrollableListCore) Close() ScrollableListCore { sl.State = ScrollableListClosed; return sl }

// WithItemsLen sets the number of items in the parent list for selection bounds.
func (sl ScrollableListCore) WithItemsLen(n int) ScrollableListCore {
	sl.ItemsLen = n
	return sl
}

// WithStyles updates the styles.
func (sl ScrollableListCore) WithStyles(styles *Styles) ScrollableListCore {
	sl.Styles = styles
	return sl
}

// When the app loses focus, all UI elements should be dimmed.
func (sl ScrollableListCore) WithFocus(hasFocus bool) ScrollableListCore {
	sl.HasFocus = hasFocus
	return sl
}

// WithSize updates the width and height of the scrollable list.
// Height is clamped to prevent the overlay from exceeding the available space.
func (sl ScrollableListCore) WithSize(width, height int) ScrollableListCore {
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

// Update handles key events for scrollable list navigation.
// ItemsLen must be set before calling Update (via WithItemsLen).
func (sl ScrollableListCore) Update(msg tea.Msg) (ScrollableListCore, ScrollableListUpdate) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return sl, ScrollableListUpdate{}
	}

	switch key.String() {
	case keyQ, keyEsc:
		sl.State = ScrollableListClosed
		return sl, ScrollableListUpdate{Handled: true, IsClose: true}
	case keyJ, keyDown:
		if sl.SelectedIdx < sl.ItemsLen-1 {
			sl.SelectedIdx++
			sl = sl.EnsureVisible()
		}
		return sl, ScrollableListUpdate{Handled: true}
	case keyK, keyUp:
		if sl.SelectedIdx > 0 {
			sl.SelectedIdx--
			sl = sl.EnsureVisible()
		}
		return sl, ScrollableListUpdate{Handled: true}
	}
	return sl, ScrollableListUpdate{}
}
