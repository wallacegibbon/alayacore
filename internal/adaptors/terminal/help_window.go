package terminal

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
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

// HelpWindowState represents the current state of the help window.
type HelpWindowState int

const (
	HelpWindowClosed HelpWindowState = iota
	HelpWindowOpen
)

// HelpWindow manages a help overlay that displays keybindings and commands.
// It provides a filter input to search items, following the same pattern
// as ModelSelector.
type HelpWindow struct {
	state         HelpWindowState
	items         []HelpItem
	filteredItems []HelpItem
	selectedIdx   int
	scrollIdx     int
	width         int
	height        int
	styles        *Styles

	// Filter state
	filterInput        textinput.Model
	filterInputFocused bool
	lastFilterValue    string

	// App focus state
	hasFocus bool

	// pendingCommand is set when Enter is pressed on a :command item.
	// Consumed by the Terminal after HandleKeyMsg returns.
	// The Terminal places it in the input field for the user to review/submit.
	pendingCommand string
}

// NewHelpWindow creates a new help window.
func NewHelpWindow(styles *Styles) *HelpWindow {
	filterInput := textinput.New()
	filterInput.Placeholder = "Filter command or key..."
	filterInput.Prompt = "/ "
	filterInput.SetWidth(50)

	hw := &HelpWindow{
		state:           HelpWindowClosed,
		items:           buildHelpItems(),
		styles:          styles,
		width:           72,
		height:          20,
		hasFocus:        true,
		filterInput:     filterInput,
		lastFilterValue: "\x00",
	}
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

		// Global Shortcuts
		{IsSection: true, Description: "Global Shortcuts"},
		{Key: "Tab", Description: "Toggle focus display/input", Type: HelpItemKey},
		{Key: "Enter", Description: "Submit prompt or command", Type: HelpItemKey},
		{Key: "Ctrl+H", Description: "Open help window", Type: HelpItemKey},
		{Key: "Ctrl+G", Description: "Cancel current request", Type: HelpItemKey},
		{Key: "Ctrl+C", Description: "Clear input field", Type: HelpItemKey},
		{Key: "Ctrl+S", Description: "Save session", Type: HelpItemKey},
		{Key: "Ctrl+O", Description: "Open external editor (input only)", Type: HelpItemKey},
		{Key: "Ctrl+L", Description: "Open model selector", Type: HelpItemKey},
		{Key: "Ctrl+P", Description: "Open theme selector", Type: HelpItemKey},
		{Key: "Ctrl+Q", Description: "Open queue manager", Type: HelpItemKey},

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

// --- State Management ---

func (hw *HelpWindow) IsOpen() bool           { return hw.state != HelpWindowClosed }
func (hw *HelpWindow) State() HelpWindowState { return hw.state }

func (hw *HelpWindow) Open() {
	hw.state = HelpWindowOpen
	hw.filterInput.SetValue("")
	hw.lastFilterValue = "\x00" // Force update
	hw.filterInputFocused = false
	hw.filterInput.Blur()
	hw.updateFilterInputStyles()
	hw.scrollIdx = 0
	hw.updateFilteredItems()
	hw.selectedIdx = hw.firstSelectableIdx()
}

func (hw *HelpWindow) Close() {
	hw.state = HelpWindowClosed
}

// --- Size & Style Management ---

func (hw *HelpWindow) SetSize(width, height int) {
	hw.width = width
	hw.height = height
	if width > 0 {
		hw.filterInput.SetWidth(max(0, width-InputPaddingH))
	}
}

func (hw *HelpWindow) SetStyles(styles *Styles) {
	hw.styles = styles
	hw.updateFilterInputStyles()
}

func (hw *HelpWindow) SetHasFocus(hasFocus bool) {
	hw.hasFocus = hasFocus
	hw.updateFilterInputStyles()
}

// updateFilterInputStyles applies current styles to the filter input.
func (hw *HelpWindow) updateFilterInputStyles() {
	hw.styles.ApplyTextInputStyles(&hw.filterInput, hw.filterInputFocused && hw.hasFocus)
}

// --- Filtering ---

// updateFilteredItems rebuilds filteredItems from items based on the current filter.
// Only non-header items are matched; section headers are included only if they
// have at least one matching item below them.
func (hw *HelpWindow) updateFilteredItems() {
	filter := strings.ToLower(hw.filterInput.Value())
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

	// TAB: Toggle focus between filter input and list
	if key == "tab" {
		hw.filterInputFocused = !hw.filterInputFocused
		if hw.filterInputFocused {
			hw.filterInput.Focus()
		} else {
			hw.filterInput.Blur()
		}
		hw.updateFilterInputStyles()
		return nil
	}

	// Route to filter input or list based on focus
	if hw.filterInputFocused {
		return hw.handleFilterInputKey(msg, key)
	}

	return hw.handleListKey(key)
}

// handleFilterInputKey handles keys when the filter input is focused.
func (hw *HelpWindow) handleFilterInputKey(msg tea.KeyMsg, key string) tea.Cmd {
	if key == "esc" {
		hw.Close()
		return nil
	}

	if key == "ctrl+c" {
		hw.filterInput.SetValue("")
		hw.updateFilteredItems()
		hw.clampSelection()
		return nil
	}

	// Ignore ctrl+u/ctrl+d to prevent textinput clear/delete behavior
	if key == "ctrl+u" || key == "ctrl+d" {
		return nil
	}

	oldValue := hw.filterInput.Value()
	var cmd tea.Cmd
	hw.filterInput, cmd = hw.filterInput.Update(msg)

	if oldValue != hw.filterInput.Value() {
		hw.updateFilteredItems()
		hw.clampSelection()
	}

	return cmd
}

// handleListKey handles keys when the list is focused.
func (hw *HelpWindow) handleListKey(key string) tea.Cmd {
	switch key {
	case "q", "esc", "ctrl+c":
		hw.Close()
		return nil

	case "enter":
		if hw.selectedIdx >= 0 && hw.selectedIdx < hw.filteredLen() {
			item := hw.filteredItems[hw.selectedIdx]
			if !item.IsSection && item.Type == HelpItemCommand {
				hw.pendingCommand = item.Key
				hw.Close()
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
	for i := hw.selectedIdx + 1; i < hw.filteredLen(); i++ {
		if !hw.filteredItems[i].IsSection {
			hw.selectedIdx = i
			hw.ensureVisible()
			return
		}
	}
}

// moveUp moves the selection up, skipping section headers.
func (hw *HelpWindow) moveUp() {
	for i := hw.selectedIdx - 1; i >= 0; i-- {
		if !hw.filteredItems[i].IsSection {
			hw.selectedIdx = i
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

// clampSelection ensures selectedIdx is within valid bounds.
func (hw *HelpWindow) clampSelection() {
	if hw.filteredLen() == 0 {
		hw.selectedIdx = 0
		hw.scrollIdx = 0
		return
	}
	if hw.selectedIdx >= hw.filteredLen() {
		hw.selectedIdx = hw.filteredLen() - 1
	}
	// Skip section headers
	if hw.selectedIdx >= 0 && hw.selectedIdx < hw.filteredLen() && hw.filteredItems[hw.selectedIdx].IsSection {
		// Try to find next non-section item
		found := false
		for i := hw.selectedIdx; i < hw.filteredLen(); i++ {
			if !hw.filteredItems[i].IsSection {
				hw.selectedIdx = i
				found = true
				break
			}
		}
		if !found {
			// Try backwards
			for i := hw.selectedIdx - 1; i >= 0; i-- {
				if !hw.filteredItems[i].IsSection {
					hw.selectedIdx = i
					break
				}
			}
		}
	}
	hw.ensureVisible()
}

// ensureVisible adjusts scrollIdx so the selected item is visible.
// The bottom-edge logic matches ThemeSelector/ModelSelector/QueueManager
// exactly (scroll when cursor moves past the last visible row).
// The top-edge keeps a 1-line margin so section headers stay visible
// when scrolling near the top of the list.
func (hw *HelpWindow) ensureVisible() {
	listHeight := SelectorListRows

	// Top edge: keep 1-line margin so section headers remain visible
	if hw.selectedIdx <= hw.scrollIdx && hw.selectedIdx > 0 {
		hw.scrollIdx = max(0, hw.selectedIdx-1)
	} else if hw.selectedIdx >= hw.scrollIdx+listHeight {
		// Bottom edge: standard logic, same as other selector windows
		hw.scrollIdx = hw.selectedIdx - listHeight + 1
	}
}

// clampScroll ensures scrollIdx is valid.
func (hw *HelpWindow) clampScroll() {
	maxScroll := max(0, hw.filteredLen()-SelectorListRows)
	if hw.scrollIdx > maxScroll {
		hw.scrollIdx = maxScroll
	}
	if hw.scrollIdx < 0 {
		hw.scrollIdx = 0
	}
}

// --- Rendering ---

// View returns the rendered help window content as a string.
func (hw *HelpWindow) View() string {
	if hw.state == HelpWindowClosed {
		return ""
	}

	listHeight := SelectorListRows
	hw.clampScroll()

	// Filter input with border
	filterBorderColor := hw.styles.BorderFocused
	if !hw.hasFocus || !hw.filterInputFocused {
		filterBorderColor = hw.styles.BorderBlurred
	}
	filterBox := hw.styles.RenderBorderedBox(hw.filterInput.View(), hw.width, filterBorderColor)

	// Build list lines
	var lines []string
	if hw.filteredLen() == 0 {
		lines = append(lines, hw.styles.System.Render("No matching commands or keys."))
	} else {
		endIdx := min(hw.scrollIdx+listHeight, hw.filteredLen())
		for i := hw.scrollIdx; i < endIdx; i++ {
			lines = append(lines, hw.renderItem(hw.filteredItems[i], i == hw.selectedIdx))
		}
	}

	// Pad to fill the list height
	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	// List with border
	listBorderColor := hw.styles.BorderFocused
	if !hw.hasFocus || hw.filterInputFocused {
		listBorderColor = hw.styles.BorderBlurred
	}
	content := strings.Join(lines, "\n")
	listBox := hw.styles.RenderBorderedBox(content, hw.width, listBorderColor, listHeight)

	// Help text based on focus
	var helpText string
	if hw.filterInputFocused {
		helpText = hw.styles.System.Render("tab: list │ esc: close")
	} else {
		base := "tab: filter │ j/k: navigate"
		if hw.selectedIdx >= 0 && hw.selectedIdx < hw.filteredLen() &&
			hw.filteredItems[hw.selectedIdx].Type == HelpItemCommand {
			base += " │ enter: copy to input"
		}
		base += " │ q/esc: close"
		helpText = hw.styles.System.Render(base)
	}

	return filterBox + "\n" + listBox + "\n" + helpText
}

// renderItem renders a single help item.
func (hw *HelpWindow) renderItem(item HelpItem, selected bool) string {
	if item.IsSection {
		return hw.styles.System.Bold(true).Render("── " + item.Description)
	}

	// Fixed 32-column key column for consistent alignment
	keyStr := fmt.Sprintf("%-32s", item.Key)
	maxDescWidth := hw.width - 36 - len("> ")
	if maxDescWidth < 4 {
		maxDescWidth = 4
	}
	desc := item.Description
	if len(desc) > maxDescWidth {
		desc = desc[:maxDescWidth-3] + "..."
	}

	if selected {
		return hw.styles.Prompt.Render("> "+keyStr) + hw.styles.Text.Render(desc)
	}
	return "  " + hw.styles.System.Render(keyStr) + hw.styles.System.Render(desc)
}

// RenderOverlay renders the help window as an overlay on top of base content.
func (hw *HelpWindow) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if hw.state == HelpWindowClosed {
		return baseContent
	}
	return renderOverlay(baseContent, hw.View(), screenWidth, screenHeight)
}
