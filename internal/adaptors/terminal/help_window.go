package terminal

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// HelpItem represents a single help entry with a key and description.
type HelpItem struct {
	Key         string
	Description string
	IsSection   bool // true for section headers
}

// HelpWindowState represents the current state of the help window.
type HelpWindowState int

const (
	HelpWindowClosed HelpWindowState = iota
	HelpWindowOpen
)

// HelpWindow manages a help overlay that displays keybindings and commands.
// It follows the same pattern as QueueManager and ThemeSelector.
type HelpWindow struct {
	state       HelpWindowState
	items       []HelpItem
	selectedIdx int
	scrollIdx   int
	width       int
	height      int
	styles      *Styles

	// App focus state
	hasFocus bool

	// pendingCommand is set when Enter is pressed on a :command item.
	// Consumed by the Terminal after HandleKeyMsg returns.
	// The Terminal places it in the input field for the user to review/submit.
	pendingCommand string
}

// NewHelpWindow creates a new help window.
func NewHelpWindow(styles *Styles) *HelpWindow {
	return &HelpWindow{
		state:    HelpWindowClosed,
		items:    buildHelpItems(),
		styles:   styles,
		width:    72,
		height:   20,
		hasFocus: true,
	}
}

// buildHelpItems constructs the static list of help entries.
func buildHelpItems() []HelpItem {
	return []HelpItem{
		// Commands
		{IsSection: true, Description: "Commands"},
		{Key: ":continue", Description: "Resume after error"},
		{Key: ":cancel", Description: "Cancel current task"},
		{Key: ":cancel_all", Description: "Cancel all & clear queue"},
		{Key: ":think", Description: "Toggle think mode"},
		{Key: ":save", Description: "Save session"},
		{Key: ":quit", Description: "Exit application"},
		{Key: ":model_set", Description: "Switch model by ID"},
		{Key: ":model_load", Description: "Reload model config"},
		{Key: ":help", Description: "Open help window"},

		// Global Shortcuts
		{IsSection: true, Description: "Global Shortcuts"},
		{Key: "Tab", Description: "Toggle focus display/input"},
		{Key: "Enter", Description: "Submit prompt or command"},
		{Key: "Ctrl+H", Description: "Open help window"},
		{Key: "Ctrl+G", Description: "Cancel current request"},
		{Key: "Ctrl+C", Description: "Clear input field"},
		{Key: "Ctrl+S", Description: "Save session"},
		{Key: "Ctrl+O", Description: "Open external editor"},
		{Key: "Ctrl+L", Description: "Open model selector"},
		{Key: "Ctrl+P", Description: "Open theme selector"},
		{Key: "Ctrl+Q", Description: "Open queue manager"},
		{Key: "Ctrl+T", Description: "Toggle think mode"},

		// Display Mode
		{IsSection: true, Description: "Display Mode"},
		{Key: "j/k", Description: "Move window cursor"},
		{Key: "J/K", Description: "Scroll one line"},
		{Key: "Ctrl+D/U", Description: "Scroll half screen"},
		{Key: "g", Description: "Go to first window"},
		{Key: "G", Description: "Go to last window, enable follow"},
		{Key: "H/L/M", Description: "Cursor top/btm/mid"},
		{Key: "e", Description: "Open in editor"},
		{Key: "f/b", Description: "Next/prev prompt"},
		{Key: ":", Description: "Enter command mode"},
		{Key: "Space", Description: "Toggle window fold"},
	}
}

// --- State Management ---

func (hw *HelpWindow) IsOpen() bool           { return hw.state != HelpWindowClosed }
func (hw *HelpWindow) State() HelpWindowState { return hw.state }

func (hw *HelpWindow) Open() {
	hw.state = HelpWindowOpen
	hw.selectedIdx = hw.firstSelectableIdx()
	hw.scrollIdx = 0
	hw.clampScroll()
}

func (hw *HelpWindow) Close() {
	hw.state = HelpWindowClosed
}

// --- Size & Style Management ---

func (hw *HelpWindow) SetSize(width, height int) {
	hw.width = width
	hw.height = height
}

func (hw *HelpWindow) SetStyles(styles *Styles) {
	hw.styles = styles
}

func (hw *HelpWindow) SetHasFocus(hasFocus bool) {
	hw.hasFocus = hasFocus
}

// --- Input Handling ---

// HandleKeyMsg processes keyboard input and returns a tea.Cmd.
func (hw *HelpWindow) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		hw.Close()
		return nil

	case "enter":
		item := hw.items[hw.selectedIdx]
		if !item.IsSection && strings.HasPrefix(item.Key, ":") {
			hw.pendingCommand = item.Key
			hw.Close()
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
	for i := hw.selectedIdx + 1; i < len(hw.items); i++ {
		if !hw.items[i].IsSection {
			hw.selectedIdx = i
			hw.ensureVisible()
			return
		}
	}
}

// moveUp moves the selection up, skipping section headers.
func (hw *HelpWindow) moveUp() {
	for i := hw.selectedIdx - 1; i >= 0; i-- {
		if !hw.items[i].IsSection {
			hw.selectedIdx = i
			hw.ensureVisible()
			return
		}
	}
}

// firstSelectableIdx returns the index of the first non-section item.
func (hw *HelpWindow) firstSelectableIdx() int {
	for i, item := range hw.items {
		if !item.IsSection {
			return i
		}
	}
	return 0
}

// ensureVisible adjusts scrollIdx so the selected item is visible.
func (hw *HelpWindow) ensureVisible() {
	listHeight := SelectorListRows
	if hw.selectedIdx < hw.scrollIdx {
		hw.scrollIdx = hw.selectedIdx
	} else if hw.selectedIdx >= hw.scrollIdx+listHeight {
		hw.scrollIdx = hw.selectedIdx - listHeight + 1
	}
}

// clampScroll ensures scrollIdx is valid.
func (hw *HelpWindow) clampScroll() {
	maxScroll := max(0, len(hw.items)-SelectorListRows)
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

	// Build visible lines
	var lines []string
	endIdx := min(hw.scrollIdx+listHeight, len(hw.items))
	for i := hw.scrollIdx; i < endIdx; i++ {
		lines = append(lines, hw.renderItem(hw.items[i], i == hw.selectedIdx))
	}

	// Pad to fill the list height
	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	// Wrap in border
	borderColor := hw.styles.BorderFocused
	if !hw.hasFocus {
		borderColor = hw.styles.BorderBlurred
	}
	content := strings.Join(lines, "\n")
	borderedBox := hw.styles.RenderBorderedBox(content, hw.width, borderColor, listHeight)

	// Help text outside the bordered box
	helpText := hw.styles.System.Render("j/k: navigate │ enter: copy command │ q/esc: close")
	return borderedBox + "\n" + helpText
}

// renderItem renders a single help item.
func (hw *HelpWindow) renderItem(item HelpItem, selected bool) string {
	if item.IsSection {
		return hw.styles.Prompt.Bold(true).Render("── " + item.Description)
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
