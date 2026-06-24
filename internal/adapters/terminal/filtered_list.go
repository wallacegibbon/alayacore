package terminal

// FilteredListCore provides shared filtering, navigation, and overlay rendering
// for selector-style UI components (ModelSelector, HelpWindow).
//
// Both components follow the same pattern:
//   - Text input for filtering
//   - Scrollable list of items below
//   - Tab toggles focus between filter and list
//   - Arrow keys navigate the list
//   - Escape/q closes
//   - Same scroll clamping, border styling, overlay positioning
//
// Embedding types call the core methods and add their own item-specific
// filtering, rendering, and key handling.
//
// SINGLE-GOROUTINE: All methods of FilteredListCore are called exclusively
// from the Bubble Tea event loop. No mutex is needed.

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
)

// FilteredListState represents the current state of a filtered list.
type FilteredListState int

const (
	FilteredListClosed FilteredListState = iota
	FilteredListOpen
)

// FilteredListCore holds shared state and methods for filtered list components.
type FilteredListCore struct {
	State       FilteredListState
	SelectedIdx int
	ScrollIdx   int
	Width       int
	Height      int
	Styles      *Styles
	HasFocus    bool

	FilterInput        *InputField
	FilterInputFocused bool
	lastFilterValue    string
}

func (fl *FilteredListCore) IsOpen() bool { return fl.State != FilteredListClosed }

// Close closes the filtered list.
func (fl *FilteredListCore) Close() { fl.State = FilteredListClosed }

// SetSize updates the width and height of the filtered list.
func (fl *FilteredListCore) SetSize(width, height int) {
	if width > 0 {
		fl.Width = width
		fl.FilterInput.SetWidth(max(0, width-InputPaddingH))
	}
	fl.Height = min(height-LayoutGap, SelectorMaxHeight)
}

// SetStyles updates the styles and re-applies them to the filter input.
func (fl *FilteredListCore) SetStyles(styles *Styles) {
	fl.Styles = styles
	fl.updateFilterInputStyles()
}

// When the app loses focus, all UI elements should be dimmed.
func (fl *FilteredListCore) SetHasFocus(hasFocus bool) {
	fl.HasFocus = hasFocus
	fl.updateFilterInputStyles()
}

// updateFilterInputStyles applies current styles to the filter input.
// The prompt (e.g. "/") uses the same color as the border so it visually
// tracks the focus state: focused border color when editing, blurred border
// color when the list is focused.
func (fl *FilteredListCore) updateFilterInputStyles() {
	fl.FilterInput.SetStyles(
		inputFieldStyle{
			Prompt:      fl.Styles.Input.Foreground(fl.Styles.BorderFocused),
			Text:        fl.Styles.Text,
			Placeholder: fl.Styles.System,
		},
		inputFieldStyle{
			Prompt:      fl.Styles.Input.Foreground(fl.Styles.BorderBlurred),
			Text:        fl.Styles.System,
			Placeholder: fl.Styles.System,
		},
		fl.Styles.CursorColor,
	)
}

// HandleTabKey toggles focus between the filter input and the list.
func (fl *FilteredListCore) HandleTabKey() {
	fl.FilterInputFocused = !fl.FilterInputFocused
	if fl.FilterInputFocused {
		fl.FilterInput.Focus()
	} else {
		fl.FilterInput.Blur()
	}
	fl.updateFilterInputStyles()
}

// HandleFilterEscape handles the escape key when the filter input is focused.
// Returns true if the component should close.
func (fl *FilteredListCore) HandleFilterEscape() bool {
	fl.State = FilteredListClosed
	return true
}

// HandleFilterCtrlC clears the filter input value.
func (fl *FilteredListCore) HandleFilterCtrlC() {
	fl.FilterInput.SetValue("")
}

// HandleKeyMsg handles common filtered list navigation keys.
// Returns (handled, filterChanged, cmd) where filterChanged indicates the filter
// input value changed and should trigger a filter update, and cmd is an optional
// tea.Cmd from the filter input.
// Component-specific keys (like Enter) are delegated to onExtra callback.
func (fl *FilteredListCore) HandleKeyMsg(msg tea.KeyMsg, onExtra func(string) bool) (handled bool, filterChanged bool, cmd tea.Cmd) {
	key := msg.String()

	if key == keyTab {
		fl.HandleTabKey()
		return true, false, nil
	}

	if fl.FilterInputFocused {
		return fl.handleFilterFocusedKey(msg, key)
	}

	return fl.handleListFocusedKey(key, onExtra)
}

// handleFilterFocusedKey handles keys when the filter input is focused.
func (fl *FilteredListCore) handleFilterFocusedKey(msg tea.KeyMsg, key string) (handled bool, filterChanged bool, cmd tea.Cmd) {
	if key == keyEsc {
		fl.State = FilteredListClosed
		return true, false, nil
	}

	if key == keyCtrlC {
		fl.HandleFilterCtrlC()
		return true, true, nil
	}

	if key == keyCtrlU || key == keyCtrlD {
		return true, false, nil
	}

	oldValue := fl.FilterInput.Value()
	fl.FilterInput, cmd = fl.FilterInput.Update(msg)
	return true, oldValue != fl.FilterInput.Value(), cmd
}

// handleListFocusedKey handles keys when the list is focused.
func (fl *FilteredListCore) handleListFocusedKey(key string, onExtra func(string) bool) (handled bool, filterChanged bool, cmd tea.Cmd) {
	switch key {
	case keyQ, keyEsc:
		fl.State = FilteredListClosed
		return true, false, nil
	case keyJ, keyDown:
		// Navigation bounds checking is done by the embedding type
		return true, false, nil
	case keyK, keyUp:
		// Navigation bounds checking is done by the embedding type
		return true, false, nil
	case keyEnter:
		if onExtra != nil && onExtra(key) {
			return true, false, nil
		}
		return true, false, nil
	}
	return false, false, nil
}

// ClampSelection clamps the selected index to valid bounds.
// This is the shared logic; embedding types may add their own constraints
// (e.g. section header skipping) after calling this.
func (fl *FilteredListCore) ClampSelection(filteredLen int) {
	if filteredLen == 0 {
		fl.SelectedIdx = 0
		fl.ScrollIdx = 0
		return
	}
	if fl.SelectedIdx >= filteredLen {
		fl.SelectedIdx = filteredLen - 1
	}
}

// EnsureVisible adjusts ScrollIdx so the selected item is visible.
// Uses the standard behavior (no top margin): if selection is above
// scroll, jump straight to it; if below, scroll to show it.
func (fl *FilteredListCore) EnsureVisible() {
	listHeight := SelectorListRows
	if fl.SelectedIdx < fl.ScrollIdx {
		fl.ScrollIdx = fl.SelectedIdx
	} else if fl.SelectedIdx >= fl.ScrollIdx+listHeight {
		fl.ScrollIdx = fl.SelectedIdx - listHeight + 1
	}
}

// ClampScroll ensures ScrollIdx is valid for the given filtered length.
func (fl *FilteredListCore) ClampScroll(filteredLen int) {
	maxScroll := max(0, filteredLen-SelectorListRows)
	if fl.ScrollIdx > maxScroll {
		fl.ScrollIdx = maxScroll
	}
	if fl.ScrollIdx < 0 {
		fl.ScrollIdx = 0
	}
}

// RenderOverlay renders the filtered list as an overlay on top of base content.
func (fl *FilteredListCore) RenderOverlay(baseContent, renderedList string, screenWidth, screenHeight int) string {
	if fl.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, renderedList, screenWidth, screenHeight)
}

// FilterBorderColor returns the border color for the filter input based on focus state.
func (fl *FilteredListCore) FilterBorderColor() color.Color {
	if !fl.HasFocus || !fl.FilterInputFocused {
		return fl.Styles.BorderBlurred
	}
	return fl.Styles.BorderFocused
}

// ListBorderColor returns the border color for the list based on focus state.
func (fl *FilteredListCore) ListBorderColor() color.Color {
	if !fl.HasFocus || fl.FilterInputFocused {
		return fl.Styles.BorderBlurred
	}
	return fl.Styles.BorderFocused
}
