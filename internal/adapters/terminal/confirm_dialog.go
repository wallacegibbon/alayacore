package terminal

// ConfirmDialog renders a centered floating overlay for confirmation dialogs.
// Used for quit, cancel, and tool_confirm prompts.
//
// The dialog uses the same rendering pattern as ModelSelector and ModelSelector:
//   - SetSize stores the terminal dimensions
//   - View renders with RenderBorderedBox
//   - RenderOverlay delegates to the shared overlay renderer
//
// Key handling: y/Y = confirm, n/N/esc = cancel.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
)

// ConfirmContentRows defines the fixed number of content lines inside
// the confirm dialog border. Matches SelectorListRows (8) used by
// ModelSelector, ModelSelector, ThemeSelector, and HelpWindow.
// If content exceeds this, it gets truncated with a "..." indicator —
// same pattern used by ModelSelector for items.
const ConfirmContentRows = 8

// ConfirmKind represents the type of active confirmation dialog.
type ConfirmKind int

const (
	ConfirmNone   ConfirmKind = iota // No dialog active
	ConfirmQuit                      // Confirm exit
	ConfirmCancel                    // Confirm cancel current request
	ConfirmTool                      // Confirm tool execution
)

// ConfirmDialog manages a floating confirmation overlay.
//
// Renders as a centered dialog box.
// For tool confirmations, the tool input is shown below the message.
//
// Key handling: y/Y = confirm, n/N/esc = cancel.
type ConfirmDialog struct {
	// Core state — follows the FilteredListCore/ScrollableListCore pattern.
	State    FilteredListState
	Kind     ConfirmKind
	HasFocus bool
	Styles   *Styles

	// Width is set by SetSize with the full terminal width, matching
	// the overlay pattern used by ModelSelector, ModelSelector, etc.
	Width  int
	Height int

	// Description shown below the title (used for all kinds).
	// Rendered as up to 2 centered rows in muted style.
	Description string

	// Tool confirm fields (only used for ConfirmTool kind)
	ToolID    string
	ToolName  string
	ToolInput string

	// Result flags — consumed by the Terminal after key handling.
	// Only one of these is set per interaction.
	Confirmed bool
	Canceled  bool
}

// NewConfirmDialog creates a new confirm dialog.
func NewConfirmDialog(styles *Styles) *ConfirmDialog {
	return &ConfirmDialog{
		Styles: styles,
	}
}

// SetSize updates the terminal dimensions for responsive sizing.
// Called on initialization and terminal resize.
func (cd *ConfirmDialog) SetSize(width, height int) {
	if width > 0 {
		cd.Width = width
	}
	cd.Height = height
}

// IsOpen returns true if the dialog is currently shown.
func (cd *ConfirmDialog) IsOpen() bool {
	return cd.State != FilteredListClosed
}

// ---- Open / Close ----

// OpenQuit opens the dialog for confirming application exit.
func (cd *ConfirmDialog) OpenQuit() {
	cd.State = FilteredListOpen
	cd.Kind = ConfirmQuit
	cd.Description = "All unsaved progress will be lost."
	cd.ToolID = ""
	cd.ToolName = ""
	cd.ToolInput = ""
	cd.Confirmed = false
	cd.Canceled = false
}

// OpenCancel opens the dialog for confirming task cancellation.
func (cd *ConfirmDialog) OpenCancel() {
	cd.State = FilteredListOpen
	cd.Kind = ConfirmCancel
	cd.Description = "The current request will be stopped."
	cd.ToolID = ""
	cd.ToolName = ""
	cd.ToolInput = ""
	cd.Confirmed = false
	cd.Canceled = false
}

// OpenTool opens the dialog for confirming a tool call.
func (cd *ConfirmDialog) OpenTool(toolID, toolName, toolInput string) {
	cd.State = FilteredListOpen
	cd.Kind = ConfirmTool
	cd.ToolID = toolID
	cd.ToolName = toolName
	cd.ToolInput = toolInput
	// Derive description from tool input (up to 2 line-break segments).
	// HardWrap in buildContentLines handles wrapping long lines, and the
	// 2-row cap with "..." handles overflow beyond 2 rows.
	// Strip the redundant "toolName: " prefix since it's already shown in the title.
	parts := strings.SplitN(toolInput, "\n", 2)
	desc := strings.Join(parts, "\n")
	if toolName != "" && strings.HasPrefix(desc, toolName+": ") {
		desc = desc[len(toolName)+2:]
	}
	// Strip trailing newline — FormatCall adds one for display window layout,
	// but in the confirm dialog it creates a spurious empty line that triggers
	// the 2-row "..." truncation prematurely.
	desc = strings.TrimRight(desc, "\n")
	cd.Description = desc
	cd.Confirmed = false
	cd.Canceled = false
}

// Close closes the dialog without committing any action.
// This is equivalent to the user pressing esc.
func (cd *ConfirmDialog) Close() {
	cd.State = FilteredListClosed
	cd.Kind = ConfirmNone
	cd.Description = ""
	cd.ToolID = ""
	cd.ToolName = ""
	cd.ToolInput = ""
	cd.Confirmed = false
	cd.Canceled = false
}

// ---- Key Handling ----

// HandleKeyMsg processes a key press and updates state.
// Returns true if the key was consumed by the dialog.
func (cd *ConfirmDialog) HandleKeyMsg(msg tea.KeyMsg) bool {
	if !cd.IsOpen() {
		return false
	}

	key := msg.String()

	switch key {
	case keyY, keyYCapital:
		cd.Confirmed = true
		cd.State = FilteredListClosed
		return true

	case keyN, keyNCapital, keyEsc:
		cd.Canceled = true
		cd.State = FilteredListClosed
		return true
	}

	// All other keys are consumed while the dialog is open
	return true
}

// ConsumeResult returns the result flags and resets them.
// Returns (confirmed, canceled).
func (cd *ConfirmDialog) ConsumeResult() (bool, bool) {
	confirmed := cd.Confirmed
	canceled := cd.Canceled
	cd.Confirmed = false
	cd.Canceled = false
	return confirmed, canceled
}

// ---- Rendering ----

// Uses RenderBorderedBox for consistent styling with other overlays.
// The dialog has a fixed content height (ConfirmContentRows) matching
func (cd *ConfirmDialog) View() tea.View {
	if !cd.IsOpen() {
		return tea.NewView("")
	}

	// Build the message lines (pre-styled with Confirm/System styles)
	msgLines := cd.buildContentLines()

	// Pad to the fixed content height, same as ModelSelector
	for len(msgLines) < ConfirmContentRows {
		msgLines = append(msgLines, "")
	}

	// Join with newlines
	content := strings.Join(msgLines, "\n")

	// Render with bordered box — border uses error color for visual warning.
	// Pass fixed height so the window is always the same size, same as
	// ModelSelector and ModelSelector overlays.
	box := cd.Styles.RenderBorderedBox(content, cd.Width, cd.Styles.ColorError, ConfirmContentRows)

	return tea.NewView("\n" + box + "\n")
}

// buildContentLines returns the display lines for the dialog content,
// with styles already applied (Confirm for the title, System for description/hints).
// (Borders are applied by View via RenderBorderedBox.)
//
// Every variant uses the same 3-part layout:
//
//	[spacing]  [title]  [spacing]  [description row 1]  [description row 2]  [spacing]
//	[y / n]  [trailing empty]
//
// The description always occupies exactly 2 rows. If the text is shorter, the
// second row is empty. If it wraps beyond 2 rows, it's truncated with "...".
func (cd *ConfirmDialog) buildContentLines() []string {
	innerWidth := max(0, cd.Width-BorderInnerPadding)
	maxBodyLines := max(0, ConfirmContentRows-2)

	titleText := cd.buildTitleText()
	if titleText == "" {
		return nil
	}

	titleLine := cd.renderTitleLine(titleText, innerWidth)
	descRows := cd.renderDescriptionRows(innerWidth)

	body := []string{""}
	body = append(body, titleLine)
	body = append(body, "")
	body = append(body, descRows...)
	body = append(body, "")

	// If the body exceeds the available rows, truncate from the bottom.
	if len(body) > maxBodyLines {
		body = body[:maxBodyLines-1]
		body = append(body, cd.wrapAndCenter("...", cd.Styles.System, innerWidth)[0])
	}

	for len(body) < maxBodyLines {
		body = append(body, "")
	}

	lines := body
	lines = append(lines, cd.wrapAndCenter("y / n", cd.Styles.System, innerWidth)[0])
	lines = append(lines, "")

	return lines
}

// buildTitleText returns the title string for the current dialog kind.
func (cd *ConfirmDialog) buildTitleText() string {
	switch cd.Kind {
	case ConfirmQuit:
		return "Exit AlayaCore?"
	case ConfirmCancel:
		return "Cancel current task?"
	case ConfirmTool:
		msg := "Allow "
		if cd.ToolName != "" {
			msg += fmt.Sprintf("%q", cd.ToolName)
		} else {
			msg += "this tool"
		}
		return msg + " to run?"
	default:
		return ""
	}
}

// renderTitleLine hard-wraps the styled title, takes only 1 row.
// If it overflows, truncates and appends "...", then centers it.
func (cd *ConfirmDialog) renderTitleLine(titleText string, innerWidth int) string {
	styled := cd.Styles.Confirm.Render(titleText)
	wrapped := ansi.Hardwrap(styled, innerWidth, false)
	lines := strings.Split(wrapped, "\n")

	line := lines[0]
	if len(lines) > 1 {
		limit := max(0, innerWidth-3)
		if ansi.StringWidth(line) > limit {
			line = ansi.Truncate(line, limit, "")
		}
		line += "..."
	}

	w := lipgloss.Width(line)
	pad := max(0, (innerWidth-w)/2)
	return strings.Repeat(" ", pad) + line + strings.Repeat(" ", innerWidth-w-pad)
}

// renderDescriptionRows hard-wraps the description, takes at most 2 rows.
// If it overflows, the second row is truncated with "...". Returns 2
// centered, styled strings suitable for appending to the body.
func (cd *ConfirmDialog) renderDescriptionRows(innerWidth int) []string {
	rawWrapped := ansi.Hardwrap(cd.Description, innerWidth, false)
	rawLines := strings.Split(rawWrapped, "\n")

	rawDesc := rawLines
	if len(rawDesc) > 2 {
		rawDesc = rawDesc[:2]
		limit := max(0, innerWidth-3)
		if ansi.StringWidth(rawDesc[1]) > limit {
			rawDesc[1] = ansi.Truncate(rawDesc[1], limit, "")
		}
		rawDesc[1] += "..."
	}
	for len(rawDesc) < 2 {
		rawDesc = append(rawDesc, "")
	}

	styled := make([]string, 2)
	for i, line := range rawDesc {
		styled[i] = cd.Styles.System.Render(line)
	}

	maxW := 0
	for _, line := range styled {
		if w := lipgloss.Width(line); w > maxW {
			maxW = w
		}
	}
	pad := max(0, (innerWidth-maxW)/2)
	rows := make([]string, 2)
	for i, line := range styled {
		w := lipgloss.Width(line)
		rows[i] = strings.Repeat(" ", pad) + line + strings.Repeat(" ", maxW-w)
	}
	return rows
}

// wrapAndCenter styles, wraps, and centers text for display in the dialog.
// When text wraps to multiple lines, the entire block is centered as a unit
// so all lines share the same left margin — no jagged alignment.
func (cd *ConfirmDialog) wrapAndCenter(text string, style lipgloss.Style, width int) []string {
	// Style the text
	styled := style.Render(text)

	// Wrap at the given width
	wrapped := wrapContent(styled, width)

	// Split into lines
	rawLines := strings.Split(wrapped, "\n")

	// Find the widest line (in display columns)
	maxLineWidth := 0
	for _, line := range rawLines {
		w := lipgloss.Width(line)
		if w > maxLineWidth {
			maxLineWidth = w
		}
	}

	// Pad all lines to the same width, then add identical left padding
	// so the whole block is centered as one unit.
	blockPadding := max(0, (width-maxLineWidth)/2)
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		w := lipgloss.Width(line)
		rightPad := maxLineWidth - w
		// Add right padding so all lines are equal width, then left padding for centering
		lines = append(lines, strings.Repeat(" ", blockPadding)+line+strings.Repeat(" ", rightPad))
	}
	return lines
}

// RenderOverlay renders the dialog as a centered overlay on top of base content.
// Uses the same renderOverlay function as all other overlays.
func (cd *ConfirmDialog) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if !cd.IsOpen() {
		return baseContent
	}
	return renderOverlay(baseContent, cd.View().Content, screenWidth, screenHeight)
}
