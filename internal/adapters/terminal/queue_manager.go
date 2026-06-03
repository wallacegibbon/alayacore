package terminal

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ansi "github.com/charmbracelet/x/ansi"
)

// QueueItem represents a queued task for display
type QueueItem struct {
	QueueID   string    `json:"queue_id"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// QueueManager manages the task queue UI.
type QueueManager struct {
	ScrollableListCore
	items []QueueItem
}

// NewQueueManager creates a new queue manager
func NewQueueManager(styles *Styles) *QueueManager {
	qm := &QueueManager{
		items: []QueueItem{},
	}
	qm.Width = 60
	qm.Height = 20
	qm.HasFocus = true
	qm.Styles = styles
	return qm
}

// --- State Management ---

func (qm *QueueManager) SetItems(items []QueueItem) {
	qm.items = items
	qm.ClampSelection(len(qm.items))
}

func (qm *QueueManager) Open() {
	qm.State = ScrollableListOpen
	qm.SelectedIdx = 0
	qm.ScrollIdx = 0
	qm.ClampSelection(len(qm.items))
}

// --- Selection Management ---

func (qm *QueueManager) GetSelectedItem() *QueueItem {
	if len(qm.items) == 0 || qm.SelectedIdx >= len(qm.items) {
		return nil
	}
	return &qm.items[qm.SelectedIdx]
}

// --- Input Handling ---

// HandleKeyMsg processes keyboard input and returns a tea.Cmd.
// Common navigation keys (j/k/q/esc/up/down) are handled by ScrollableListCore.
// Keys d and e are not handled here — the parent (Terminal) intercepts them.
func (qm *QueueManager) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	handled, _ := qm.ScrollableListCore.HandleKeyMsg(msg, len(qm.items))
	if handled {
		return nil
	}
	return nil
}

// --- Rendering ---

func (qm *QueueManager) View() tea.View {
	if qm.State == ScrollableListClosed {
		return tea.NewView("")
	}

	listHeight := SelectorListRows // content rows inside border
	maxItems := SelectorListRows   // All rows for items

	var lines []string

	if len(qm.items) == 0 {
		emptyStyle := qm.Styles.System
		lines = append(lines, emptyStyle.Render("  No queued tasks"))
	} else {
		qm.updateScrollForHeight(maxItems)

		endIdx := qm.ScrollIdx + maxItems
		if endIdx > len(qm.items) {
			endIdx = len(qm.items)
		}

		for i := qm.ScrollIdx; i < endIdx; i++ {
			item := qm.items[i]
			lines = append(lines, qm.renderItem(item, i == qm.SelectedIdx))
		}
	}

	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	borderColor := qm.ListBorderColor()
	content := strings.Join(lines, "\n")
	borderedBox := qm.Styles.RenderBorderedBox(content, qm.Width, borderColor, listHeight)

	// Title bar with background
	titleStyle := lipgloss.NewStyle().Background(qm.Styles.ColorDim).Foreground(qm.Styles.ColorAccent).Bold(true)
	title := titleStyle.Render(fmt.Sprintf("%-*s", qm.Width, "  Task Queue"))

	// Help bar with background
	helpStyle := lipgloss.NewStyle().Background(qm.Styles.ColorDim).Foreground(qm.Styles.ColorMuted)
	help := helpStyle.Render(fmt.Sprintf("%-*s", qm.Width, "  j/k: navigate │ d: delete │ e: edit │ q/esc: close"))

	return tea.NewView(title + "\n" + borderedBox + "\n" + help)
}

func (qm *QueueManager) updateScrollForHeight(height int) {
	if qm.SelectedIdx >= qm.ScrollIdx+height {
		qm.ScrollIdx = qm.SelectedIdx - height + 1
	}

	if qm.SelectedIdx < qm.ScrollIdx {
		qm.ScrollIdx = qm.SelectedIdx
	}
}

func (qm *QueueManager) renderItem(item QueueItem, selected bool) string {
	maxWidth := qm.Width - 6
	if maxWidth < 10 {
		maxWidth = 10
	}

	content := item.Content
	content = strings.ReplaceAll(content, "\n", "\\n")
	content = strings.ReplaceAll(content, "\t", "\\t")

	if item.Type == "command" {
		content = ":" + content
	}

	truncated := ansi.Hardwrap(content, maxWidth, false)
	if truncated != content {
		truncated = ansi.Hardwrap(content, maxWidth-3, false)
		content = strings.SplitN(truncated, "\n", 2)[0] + "..."
	}

	if selected {
		return qm.Styles.Prompt.Render("> ") + qm.Styles.Text.Render(content)
	}
	return qm.Styles.System.Render("  " + content)
}

// RenderOverlay renders the queue manager as an overlay on top of base content
func (qm *QueueManager) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if qm.State == ScrollableListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, qm.View().Content, screenWidth, screenHeight)
}
