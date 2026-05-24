package terminal

// HelpWindow manages a help overlay that displays keybindings and commands.
// It provides a filter input to search items, following the same pattern
// as ModelSelector.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
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
	hw.updateFilteredItems()
	return hw
}

// buildHelpItems constructs the static list of help entries.
func buildHelpItems() []HelpItem {
	return []HelpItem{
		// Commands
		{IsSection: true, Description: "Commands"},
		{Key: ":continue", Description: "Resume after error", Type: HelpItemCommand},
		{Key: ":cancel", Description: "Cancel current task", Type: HelpItemCommand},
		{Key: ":cancel_all", Description: "Cancel all & clear queue", Type: HelpItemCommand},
		{Key: ":summarize", Description: "Summarize conversation", Type: HelpItemCommand},
		{Key: ":think", Description: "Set think level (0/1/2)", Type: HelpItemCommand},
		{Key: ":save", Description: "Save session", Type: HelpItemCommand},
		{Key: ":quit", Description: "Exit application", Type: HelpItemCommand},
		{Key: ":model_set", Description: "Switch model by ID", Type: HelpItemCommand},
		{Key: ":model_load", Description: "Reload model config", Type: HelpItemCommand},
		{Key: ":help", Description: "Open help window", Type: HelpItemCommand},
		{Key: ":suspend", Description: "Suspend process (Ctrl+Z)", Type: HelpItemCommand},

		// Global Shortcuts
		{IsSection: true, Description: "Global Shortcuts"},
		{Key: "Tab", Description: "Toggle focus display/input", Type: HelpItemKey},
		{Key: "Enter", Description: "Submit prompt or command", Type: HelpItemKey},
		{Key: "Ctrl+H", Description: "Open help window", Type: HelpItemKey},
		{Key: "Ctrl+G", Description: "Cancel current request", Type: HelpItemKey},
		{Key: "Ctrl+C", Description: "Clear input field (input only)", Type: HelpItemKey},
		{Key: "Ctrl+S", Description: "Save session", Type: HelpItemKey},
		{Key: "Ctrl+O", Description: "Open external editor (input only)", Type: HelpItemKey},
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
		{Key: "G", Description: "Go to last window, enable follow", Type: HelpItemKey},
		{Key: "H/L/M", Description: "Cursor top/btm/mid", Type: HelpItemKey},
		{Key: "e", Description: "Open in editor", Type: HelpItemKey},
		{Key: "f/b", Description: "Next/prev prompt", Type: HelpItemKey},
		{Key: ":", Description: "Enter command mode", Type: HelpItemKey},
		{Key: "Space", Description: "Toggle window fold", Type: HelpItemKey},
	}
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

	if key == "tab" {
		hw.HandleTabKey()
		return nil
	}

	if hw.FilterInputFocused {
		return hw.handleFilterInputKey(msg, key)
	}

	return hw.handleListKey(key)
}

// handleFilterInputKey handles keys when the filter input is focused.
func (hw *HelpWindow) handleFilterInputKey(msg tea.KeyMsg, key string) tea.Cmd {
	if key == "esc" {
		hw.State = FilteredListClosed
		return nil
	}

	if key == "ctrl+c" {
		hw.HandleFilterCtrlC()
		hw.updateFilteredItems()
		hw.ClampSelection(hw.filteredLen())
		hw.skipSectionHeaders()
		return nil
	}

	if key == "ctrl+u" || key == "ctrl+d" {
		return nil
	}

	oldValue := hw.FilterInput.Value()
	var cmd tea.Cmd
	hw.FilterInput, cmd = hw.FilterInput.Update(msg)

	if oldValue != hw.FilterInput.Value() {
		hw.updateFilteredItems()
		hw.ClampSelection(hw.filteredLen())
		hw.skipSectionHeaders()
	}

	return cmd
}

// handleListKey handles keys when the list is focused.
func (hw *HelpWindow) handleListKey(key string) tea.Cmd {
	switch key {
	case "q", "esc", "ctrl+c":
		hw.State = FilteredListClosed
		return nil

	case "enter":
		if hw.SelectedIdx >= 0 && hw.SelectedIdx < hw.filteredLen() {
			item := hw.filteredItems[hw.SelectedIdx]
			if !item.IsSection && item.Type == HelpItemCommand {
				hw.pendingCommand = item.Key
				hw.State = FilteredListClosed
			}
		}
		return nil

	case "j", "down":
		hw.moveDown()
		return nil

	case "k", "up":
		hw.moveUp()
		return nil
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

	var helpText string
	if hw.FilterInputFocused {
		helpText = hw.Styles.System.Render("tab: list │ esc: close")
	} else {
		base := "tab: filter │ j/k: navigate"
		if hw.SelectedIdx >= 0 && hw.SelectedIdx < hw.filteredLen() &&
			hw.filteredItems[hw.SelectedIdx].Type == HelpItemCommand {
			base += " │ enter: copy to input"
		}
		base += " │ q/esc: close"
		helpText = hw.Styles.System.Render(base)
	}

	return tea.NewView(filterBox + "\n" + listBox + "\n" + helpText)
}

// renderItem renders a single help item.
func (hw *HelpWindow) renderItem(item HelpItem, selected bool) string {
	if item.IsSection {
		return hw.Styles.System.Bold(true).Render("── " + item.Description)
	}

	keyStr := fmt.Sprintf("%-32s", item.Key)
	maxDescWidth := hw.Width - 36 - len("> ")
	if maxDescWidth < 4 {
		maxDescWidth = 4
	}
	desc := item.Description
	if len(desc) > maxDescWidth {
		desc = desc[:maxDescWidth-3] + "..."
	}

	if selected {
		return hw.Styles.Prompt.Render("> "+keyStr) + hw.Styles.Text.Render(desc)
	}
	return "  " + hw.Styles.System.Render(keyStr) + hw.Styles.System.Render(desc)
}

// RenderOverlay renders the help window as an overlay on top of base content.
func (hw *HelpWindow) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	return hw.FilteredListCore.RenderOverlay(baseContent, hw.View().Content, screenWidth, screenHeight)
}

// Bubble Tea interface (unused — routing goes through Terminal)
func (hw *HelpWindow) Init() tea.Cmd { return nil }

func (hw *HelpWindow) Update(_ tea.Msg) (tea.Model, tea.Cmd) {
	if hw.State == FilteredListClosed {
		return hw, nil
	}
	return hw, nil
}

var _ tea.Model = (*HelpWindow)(nil)
