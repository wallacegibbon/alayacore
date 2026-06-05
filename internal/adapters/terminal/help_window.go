package terminal

// HelpWindow manages a help overlay that displays keybindings and commands.
// It provides a filter input to search items, following the same pattern
// as ModelSelector.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
)

// FuzzyMatchHelpItem checks if the search term fuzzy-matches either the Key
// or Description field of a HelpItem (case-insensitive).
func FuzzyMatchHelpItem(search string, item HelpItem) bool {
	if search == "" {
		return true
	}
	return FuzzyMatch(search, strings.ToLower(item.Key)) ||
		FuzzyMatch(search, strings.ToLower(item.Description))
}

// HelpItemType classifies a HelpItem as either a command or a key binding.
type HelpItemType int

const (
	HelpItemKey     HelpItemType = iota // key binding (e.g. "j", "Ctrl+H")
	HelpItemCommand                     // command (e.g. ":quit", ":save")
)

// HelpItem represents a single help entry with a key and description.
type HelpItem struct {
	Key         string
	Description string
	IsSection   bool         // true for section headers
	Type        HelpItemType // HelpItemCommand for :commands, HelpItemKey for key bindings
}

// HelpWindow manages a help overlay that displays keybindings and commands.
type HelpWindow struct {
	FilteredListCore

	items         []HelpItem
	filteredItems []HelpItem

	// pendingCommand is set when Enter is pressed on a :command item.
	// Consumed by the Terminal after HandleKeyMsg returns.
	pendingCommand string

	// keyColumnWidth is the fixed width of the key column, computed from the
	// longest key and description so that all descriptions align vertically
	// and the longest description reaches the right edge of the window.
	keyColumnWidth int
}

// NewHelpWindow creates a new help window.
func NewHelpWindow(styles *Styles) *HelpWindow {
	input := newFilterInput("Filter command or key...")
	hw := &HelpWindow{
		items: buildHelpItems(),
	}
	hw.Width = 72
	hw.Height = 20
	hw.HasFocus = true
	hw.FilterInput = input
	hw.lastFilterValue = "\x00"
	hw.Styles = styles
	hw.recalculateColumnWidths()
	hw.updateFilteredItems()
	return hw
}

// buildHelpItems constructs the static list of help entries.
func buildHelpItems() []HelpItem {
	return []HelpItem{
		// Commands
		{IsSection: true, Description: "Commands"},
		{Key: ":confirm <yes|no>", Description: "Confirm or deny pending tool", Type: HelpItemCommand},
		{Key: ":continue [skip]", Description: "Retry / skip failed prompt", Type: HelpItemCommand},
		{Key: ":reason <0|1|2>", Description: "Set reasoning level", Type: HelpItemCommand},
		{Key: ":cancel_all", Description: "Cancel all & clear queue", Type: HelpItemCommand},
		{Key: ":cancel", Description: "Cancel current task", Type: HelpItemCommand},
		{Key: ":summarize", Description: "Summarize & compress history", Type: HelpItemCommand},
		{Key: ":theme_set <name>", Description: "Switch theme by name", Type: HelpItemCommand},
		{Key: ":model_set <id>", Description: "Switch model by ID", Type: HelpItemCommand},
		{Key: ":model_load", Description: "Reload model config", Type: HelpItemCommand},
		{Key: ":save [filename]", Description: "Save session", Type: HelpItemCommand},
		{Key: ":suspend", Description: "Suspend process", Type: HelpItemCommand},
		{Key: ":quit", Description: "Exit application", Type: HelpItemCommand},
		{Key: ":help", Description: "Open help window", Type: HelpItemCommand},

		// Global Shortcuts
		{IsSection: true, Description: "Global Shortcuts"},
		{Key: "Tab", Description: "Toggle focus display/input", Type: HelpItemKey},
		{Key: "Enter", Description: "Submit prompt or command", Type: HelpItemKey},
		{Key: "Ctrl+H", Description: "Open help window", Type: HelpItemKey},
		{Key: "Ctrl+G", Description: "Cancel current task", Type: HelpItemKey},
		{Key: "Ctrl+C", Description: "Clear text", Type: HelpItemKey},
		{Key: "Ctrl+S", Description: "Save session", Type: HelpItemKey},
		{Key: "Ctrl+O", Description: "Open in editor (main input)", Type: HelpItemKey},
		{Key: "Ctrl+L", Description: "Open model selector", Type: HelpItemKey},
		{Key: "Ctrl+P", Description: "Open theme selector", Type: HelpItemKey},
		{Key: "Ctrl+Q", Description: "Open queue manager", Type: HelpItemKey},
		{Key: "Ctrl+Z", Description: "Suspend process", Type: HelpItemKey},

		// Queue Manager
		{IsSection: true, Description: "Queue Manager"},
		{Key: "j/k", Description: "Navigate queue items", Type: HelpItemKey},
		{Key: "d", Description: "Delete selected queue item", Type: HelpItemKey},
		{Key: "e", Description: "Edit selected item in editor", Type: HelpItemKey},
		{Key: "q/esc", Description: "Close queue manager", Type: HelpItemKey},

		// Display Mode
		{IsSection: true, Description: "Display Mode"},
		{Key: "j/k", Description: "Move window cursor", Type: HelpItemKey},
		{Key: "J/K", Description: "Scroll one line", Type: HelpItemKey},
		{Key: "Ctrl+D/U", Description: "Scroll half screen", Type: HelpItemKey},
		{Key: "g", Description: "Go to first window", Type: HelpItemKey},
		{Key: "G", Description: "Follow the last window", Type: HelpItemKey},
		{Key: "H/L/M", Description: "Cursor top/btm/mid", Type: HelpItemKey},
		{Key: "e", Description: "Open in editor", Type: HelpItemKey},
		{Key: "f/b", Description: "Next/prev prompt", Type: HelpItemKey},
		{Key: ":", Description: "Enter command mode", Type: HelpItemKey},
		{Key: "Space", Description: "Toggle window fold", Type: HelpItemKey},
	}
}

// --- Column Widths ---

// recalculateColumnWidths computes keyColumnWidth so that all descriptions
// align at the same column and the longest description reaches the right edge.
//
// Layout per row: prefix(2) + keyPadded + " " + desc
// We want: 2 + keyColumnWidth + 1 + maxDescLen = innerWidth
// So: keyColumnWidth = innerWidth - 3 - maxDescLen
//
// If the window is too narrow, keyColumnWidth is at least maxKeyLen.
func (hw *HelpWindow) recalculateColumnWidths() {
	maxKeyLen := 0
	maxDescLen := 0
	for _, item := range hw.items {
		if !item.IsSection {
			if w := lipgloss.Width(item.Key); w > maxKeyLen {
				maxKeyLen = w
			}
			if w := lipgloss.Width(item.Description); w > maxDescLen {
				maxDescLen = w
			}
		}
	}

	innerWidth := max(0, hw.Width-BorderInnerPadding)
	idealKeyWidth := innerWidth - 3 - maxDescLen

	// Must fit the longest key
	if idealKeyWidth < maxKeyLen {
		idealKeyWidth = maxKeyLen
	}

	hw.keyColumnWidth = max(1, idealKeyWidth)
}

// SetSize sets the size of the help window and recalculates column widths.
func (hw *HelpWindow) SetSize(width, height int) {
	hw.FilteredListCore.SetSize(width, height)
	hw.recalculateColumnWidths()
}

// --- Open / Close ---

func (hw *HelpWindow) Open() {
	hw.State = FilteredListOpen
	hw.FilterInput.SetValue("")
	hw.lastFilterValue = "\x00"
	hw.FilterInputFocused = false
	hw.FilterInput.Blur()
	hw.updateFilterInputStyles()
	hw.ScrollIdx = 0
	hw.updateFilteredItems()
	hw.SelectedIdx = hw.firstSelectableIdx()
}

// --- Filtering ---

// updateFilteredItems rebuilds filteredItems from items based on the current filter.
// Only non-header items are matched; section headers are included only if they
// have at least one matching item below them.
func (hw *HelpWindow) updateFilteredItems() {
	filter := strings.ToLower(hw.FilterInput.Value())
	if filter == hw.lastFilterValue {
		return
	}
	hw.lastFilterValue = filter

	if filter == "" {
		hw.filteredItems = hw.items
		return
	}

	var result []HelpItem
	var currentSection []HelpItem
	var sectionHeader *HelpItem

	flushSection := func() {
		if sectionHeader != nil && len(currentSection) > 0 {
			result = append(result, *sectionHeader)
			result = append(result, currentSection...)
		}
		sectionHeader = nil
		currentSection = nil
	}

	for _, item := range hw.items {
		if item.IsSection {
			flushSection()
			h := item
			sectionHeader = &h
			continue
		}
		if FuzzyMatchHelpItem(filter, item) {
			currentSection = append(currentSection, item)
		}
	}
	flushSection()

	hw.filteredItems = result
}

// filteredLen returns the length of filteredItems.
func (hw *HelpWindow) filteredLen() int {
	return len(hw.filteredItems)
}

// --- Input Handling ---

// HandleKeyMsg processes keyboard input and returns a tea.Cmd.
func (hw *HelpWindow) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()

	// Common filtered list handling (tab, esc, ctrl+c, filter input keys)
	handled, filterChanged, cmd := hw.FilteredListCore.HandleKeyMsg(msg, func(extraKey string) bool {
		// Called for Enter when list is focused
		if extraKey == keyEnter {
			if hw.SelectedIdx >= 0 && hw.SelectedIdx < hw.filteredLen() {
				item := hw.filteredItems[hw.SelectedIdx]
				if !item.IsSection && item.Type == HelpItemCommand {
					// Extract just the command name, strip argument syntax
					parts := strings.Fields(item.Key)
					if len(parts) > 0 {
						hw.pendingCommand = parts[0]
					} else {
						hw.pendingCommand = item.Key
					}
					hw.State = FilteredListClosed
				}
			}
			return true
		}
		return false
	})

	if handled {
		if filterChanged {
			hw.updateFilteredItems()
			hw.clampSelection()
		}

		// If list is focused and j/k was pressed, use section-aware navigation
		if !hw.FilterInputFocused && (key == keyJ || key == keyDown) {
			hw.moveDown()
		} else if !hw.FilterInputFocused && (key == keyK || key == keyUp) {
			hw.moveUp()
		}

		return cmd
	}

	return nil
}

// ConsumePendingCommand returns the pending command (if any) and clears it.
func (hw *HelpWindow) ConsumePendingCommand() string {
	cmd := hw.pendingCommand
	hw.pendingCommand = ""
	return cmd
}

// --- Navigation ---

// moveDown moves the selection down, skipping section headers.
func (hw *HelpWindow) moveDown() {
	for i := hw.SelectedIdx + 1; i < hw.filteredLen(); i++ {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			hw.ensureVisible()
			return
		}
	}
}

// moveUp moves the selection up, skipping section headers.
func (hw *HelpWindow) moveUp() {
	for i := hw.SelectedIdx - 1; i >= 0; i-- {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			hw.ensureVisible()
			return
		}
	}
}

// firstSelectableIdx returns the index of the first non-section item.
func (hw *HelpWindow) firstSelectableIdx() int {
	for i, item := range hw.filteredItems {
		if !item.IsSection {
			return i
		}
	}
	return 0
}

// skipSectionHeaders ensures the selected item is not a section header.
func (hw *HelpWindow) skipSectionHeaders() {
	if hw.SelectedIdx < 0 || hw.SelectedIdx >= hw.filteredLen() {
		return
	}
	if !hw.filteredItems[hw.SelectedIdx].IsSection {
		return
	}
	// Try forward first, then backward
	for i := hw.SelectedIdx; i < hw.filteredLen(); i++ {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			return
		}
	}
	for i := hw.SelectedIdx - 1; i >= 0; i-- {
		if !hw.filteredItems[i].IsSection {
			hw.SelectedIdx = i
			return
		}
	}
}

// clampSelection ensures selectedIdx is within valid bounds.
func (hw *HelpWindow) clampSelection() {
	hw.ClampSelection(hw.filteredLen())
	hw.skipSectionHeaders()
	hw.ensureVisible()
}

// ensureVisible adjusts ScrollIdx so the selected item is visible.
// Keeps a 1-line margin so section headers remain visible when
// scrolling near the top of the list.
func (hw *HelpWindow) ensureVisible() {
	listHeight := SelectorListRows

	if hw.SelectedIdx <= hw.ScrollIdx && hw.SelectedIdx > 0 {
		hw.ScrollIdx = max(0, hw.SelectedIdx-1)
	} else if hw.SelectedIdx >= hw.ScrollIdx+listHeight {
		hw.ScrollIdx = hw.SelectedIdx - listHeight + 1
	}
}

// --- Rendering ---

// View returns the rendered help window content as a string.
func (hw *HelpWindow) View() tea.View {
	if hw.State == FilteredListClosed {
		return tea.NewView("")
	}

	listHeight := SelectorListRows
	hw.ClampScroll(hw.filteredLen())

	filterBox := hw.Styles.RenderBorderedBox(hw.FilterInput.View(), hw.Width, hw.FilterBorderColor())

	var lines []string
	if hw.filteredLen() == 0 {
		lines = append(lines, hw.Styles.System.Render("No matching commands or keys."))
	} else {
		endIdx := min(hw.ScrollIdx+listHeight, hw.filteredLen())
		for i := hw.ScrollIdx; i < endIdx; i++ {
			lines = append(lines, hw.renderItem(hw.filteredItems[i], i == hw.SelectedIdx))
		}
	}

	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	listBorderColor := hw.ListBorderColor()
	content := strings.Join(lines, "\n")
	listBox := hw.Styles.RenderBorderedBox(content, hw.Width, listBorderColor, listHeight)

	// Title bar with background
	titleStyle := lipgloss.NewStyle().Background(hw.Styles.ColorDim).Foreground(hw.Styles.ColorAccent).Bold(true)
	title := titleStyle.Render(fmt.Sprintf("%-*s", hw.Width, "  Help"))

	// Help bar with background
	helpStyle := lipgloss.NewStyle().Background(hw.Styles.ColorDim).Foreground(hw.Styles.ColorMuted)
	var help string
	if hw.FilterInputFocused {
		help = "  tab: list │ esc: close"
	} else {
		base := "tab: filter │ j/k: navigate"
		if hw.SelectedIdx >= 0 && hw.SelectedIdx < hw.filteredLen() &&
			hw.filteredItems[hw.SelectedIdx].Type == HelpItemCommand {
			base += " │ enter: copy to input"
		}
		base += " │ q/esc: close"
		help = "  " + base
	}
	helpBar := helpStyle.Render(fmt.Sprintf("%-*s", hw.Width, help))

	return tea.NewView(title + "\n" + filterBox + "\n" + listBox + "\n" + helpBar)
}

// renderItem renders a single help item using the same ansi.Hardwrap
// truncation pattern as QueueManager.renderItem: build the raw line,
// check if it fits at innerWidth, and truncate with "..." if not.
func (hw *HelpWindow) renderItem(item HelpItem, selected bool) string {
	innerWidth := max(0, hw.Width-BorderInnerPadding)

	if item.IsSection {
		content := "── " + item.Description
		truncated := ansi.Hardwrap(content, innerWidth, false)
		if truncated != content {
			truncated = ansi.Hardwrap(content, max(1, innerWidth-3), false)
			content = strings.SplitN(truncated, "\n", 2)[0] + "..."
		}
		return hw.Styles.System.Bold(true).Render(content)
	}

	// Build raw line with fixed key column width
	keyMaxWidth := hw.keyColumnWidth
	descMaxWidth := max(1, innerWidth-3-keyMaxWidth)

	// Truncate key first if too long (same Hardwrap pattern)
	key := item.Key
	truncated := ansi.Hardwrap(key, keyMaxWidth, false)
	if truncated != key {
		truncated = ansi.Hardwrap(key, max(1, keyMaxWidth-3), false)
		key = strings.SplitN(truncated, "\n", 2)[0] + "..."
	}

	// Truncate description if too long (same Hardwrap pattern)
	desc := item.Description
	truncated = ansi.Hardwrap(desc, descMaxWidth, false)
	if truncated != desc {
		truncated = ansi.Hardwrap(desc, max(1, descMaxWidth-3), false)
		desc = strings.SplitN(truncated, "\n", 2)[0] + "..."
	}

	// Build the full raw line: padded key + space + desc
	// Use display-width-aware padding instead of fmt.Sprintf, which pads by rune count
	// and misaligns wide characters (e.g. CJK).
	padding := max(0, keyMaxWidth-lipgloss.Width(key))
	keyPadded := key + strings.Repeat(" ", padding)
	line := keyPadded + " " + desc

	if selected {
		return hw.Styles.Prompt.Render("> ") + hw.Styles.Text.Render(line)
	}
	return hw.Styles.System.Render("  " + line)
}

// RenderOverlay renders the help window as an overlay on top of base content.
func (hw *HelpWindow) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if hw.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, hw.View().Content, screenWidth, screenHeight)
}
