package terminal

// AttachmentWindow is a file picker overlay for adding attachments to user input.
// It provides a path input field with a filtered file list, similar to ModelSelector.
// Users can type a path directly, Tab to the list to browse, and Enter to add files.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// AttachmentWindow manages file selection for multi-modal input.
type AttachmentWindow struct {
	FilteredListCore

	entries    []fileEntry
	filtered   []fileEntry
	currentDir string

	// Callback: invoked when user adds a file.
	onAdd func(path string)
}

// fileEntry represents a single file system entry.
type fileEntry struct {
	name  string
	isDir bool
}

// NewAttachmentWindow creates a new attachment picker.
func NewAttachmentWindow(styles *Styles) *AttachmentWindow {
	input := newFilterInput("Search files...")
	aw := &AttachmentWindow{
		entries:  []fileEntry{},
		filtered: []fileEntry{},
	}
	aw.Width = 60
	aw.Height = 20
	aw.HasFocus = true
	aw.FilterInput = input
	aw.lastFilterValue = "\x00"
	aw.Styles = styles
	return aw
}

// SetOnAdd sets the callback for when a file is selected.
func (aw *AttachmentWindow) SetOnAdd(fn func(path string)) {
	aw.onAdd = fn
}

// Open opens the attachment window and loads the current directory.
func (aw *AttachmentWindow) Open() {
	aw.State = FilteredListOpen
	aw.FilterInput.SetValue("")
	aw.lastFilterValue = "\x00"
	aw.FilterInputFocused = false
	aw.FilterInput.Blur()
	aw.updateFilterInputStyles()
	aw.ScrollIdx = 0
	aw.SelectedIdx = 0
	aw.currentDir, _ = os.Getwd()
	aw.loadDir(aw.currentDir)
}

func (aw *AttachmentWindow) loadDir(dir string) {
	aw.entries = aw.readDir(dir)
	aw.filtered = make([]fileEntry, len(aw.entries))
	copy(aw.filtered, aw.entries)
	aw.SelectedIdx = 0
	aw.ScrollIdx = 0
	aw.ClampSelection(len(aw.filtered))
	aw.EnsureVisible()
}

func (aw *AttachmentWindow) readDir(dir string) []fileEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	result := []fileEntry{}
	// Add parent directory entry if not at root
	if dir != "/" {
		result = append(result, fileEntry{name: "..", isDir: true})
	}
	for _, e := range entries {
		result = append(result, fileEntry{
			name:  e.Name(),
			isDir: e.IsDir(),
		})
	}
	return result
}

// HandleKeyMsg handles keyboard input.
func (aw *AttachmentWindow) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if aw.State == FilteredListClosed {
		return nil
	}

	key := msg.String()

	handled, filterChanged, cmd := aw.FilteredListCore.HandleKeyMsg(msg, func(extraKey string) bool {
		if extraKey == keyEnter {
			return aw.handleEnter()
		}
		if extraKey == keyEsc {
			aw.State = FilteredListClosed
			return true
		}
		return false
	})

	if handled {
		if filterChanged && aw.FilterInputFocused {
			aw.updateFiltered()
		}
		if !aw.FilterInputFocused {
			aw.handleListKeys(key)
		}
		if aw.FilterInputFocused && key == keyEnter && len(aw.filtered) > 0 {
			aw.handleSearchEnter()
		}
		return cmd
	}

	if !aw.FilterInputFocused {
		aw.handleListKeys(key)
	}

	return nil
}

func (aw *AttachmentWindow) handleEnter() bool {
	if len(aw.filtered) > 0 && aw.SelectedIdx >= 0 {
		return aw.selectEntry(aw.SelectedIdx)
	}
	return false
}

func (aw *AttachmentWindow) handleSearchEnter() {
	if len(aw.filtered) > 0 {
		aw.SelectedIdx = 0
		aw.selectEntry(0)
	}
}

func (aw *AttachmentWindow) selectEntry(idx int) bool {
	if idx < 0 || idx >= len(aw.filtered) {
		return false
	}
	entry := aw.filtered[idx]
	fullPath := filepath.Join(aw.currentDir, entry.name)

	if entry.name == ".." {
		// Go up
		parent := filepath.Dir(aw.currentDir)
		aw.currentDir = parent
		aw.loadDir(parent)
		aw.FilterInput.SetValue("")
		aw.lastFilterValue = "\x00"
		aw.updateFiltered()
		return true
	}

	if entry.isDir {
		// Enter directory
		aw.currentDir = fullPath
		aw.loadDir(fullPath)
		aw.FilterInput.SetValue("")
		aw.lastFilterValue = "\x00"
		aw.updateFiltered()
		return true
	}

	// It's a file — add it and close
	if aw.onAdd != nil {
		aw.onAdd(fullPath)
	}
	aw.State = FilteredListClosed
	return true
}

func (aw *AttachmentWindow) handleListKeys(key string) {
	switch key {
	case keyJ, keyDown:
		if aw.SelectedIdx < len(aw.filtered)-1 {
			aw.SelectedIdx++
		}
	case keyK, keyUp:
		if aw.SelectedIdx > 0 {
			aw.SelectedIdx--
		}
	}
}

// updateFiltered rebuilds the filtered file list based on filter input.
func (aw *AttachmentWindow) updateFiltered() {
	search := aw.FilterInput.Value()
	if search == aw.lastFilterValue {
		return
	}

	var prevSelectedID int
	if aw.SelectedIdx >= 0 && aw.SelectedIdx < len(aw.filtered) {
		prevSelectedID = aw.SelectedIdx
	}

	aw.lastFilterValue = search

	// If the filter contains a path separator, try to navigate to that directory
	if strings.Contains(search, "/") {
		// Resolve the path relative to currentDir
		candidate := filepath.Join(aw.currentDir, search)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			aw.currentDir = candidate
			aw.loadDir(candidate)
			aw.FilterInput.SetValue("")
			aw.lastFilterValue = "\x00"
			search = ""
		}
	}

	if search == "" {
		aw.filtered = make([]fileEntry, len(aw.entries))
		copy(aw.filtered, aw.entries)
	} else {
		term := strings.ToLower(search)
		aw.filtered = aw.filtered[:0]
		for _, e := range aw.entries {
			if strings.Contains(strings.ToLower(e.name), term) {
				aw.filtered = append(aw.filtered, e)
			}
		}
	}

	if prevSelectedID >= 0 && prevSelectedID < len(aw.filtered) {
		aw.SelectedIdx = prevSelectedID
	} else {
		aw.SelectedIdx = 0
	}
	aw.ClampSelection(len(aw.filtered))
	aw.EnsureVisible()
	aw.ClampScroll(len(aw.filtered))
}

// View renders the attachment window.
func (aw *AttachmentWindow) View() tea.View {
	if aw.State == FilteredListClosed {
		return tea.NewView("")
	}
	return tea.NewView(aw.render())
}

func (aw *AttachmentWindow) render() string {
	var sb strings.Builder

	// Title bar
	titleStyle := lipgloss.NewStyle().Background(aw.Styles.ColorDim).Foreground(aw.Styles.ColorAccent).Bold(true)
	sb.WriteString(titleStyle.Render(fmt.Sprintf("%-*s", aw.Width, "  Attachments")))
	sb.WriteString("\n")

	// Search box
	searchBox := aw.Styles.RenderBorderedBox(aw.FilterInput.View(), aw.Width, aw.FilterBorderColor())
	sb.WriteString(searchBox)
	sb.WriteString("\n")

	// Current directory
	sb.WriteString(aw.Styles.System.Render(aw.currentDir))
	sb.WriteString("\n")

	// File list
	boxWidth := lipgloss.Width(searchBox)
	listBorderColor := aw.ListBorderColor()
	listHeight := SelectorListRows
	innerWidth := max(0, boxWidth-BorderInnerPadding)

	var content strings.Builder
	switch {
	case len(aw.entries) == 0:
		content.WriteString(aw.Styles.System.Render("Directory is empty."))
	default:
		aw.EnsureVisible()
		for i := aw.ScrollIdx; i < min(aw.ScrollIdx+listHeight, len(aw.filtered)); i++ {
			e := aw.filtered[i]
			isSelected := i == aw.SelectedIdx

			name := e.name
			if e.isDir {
				name += "/"
			}

			truncated := truncateWithSuffix(name, max(1, innerWidth-2))
			if isSelected {
				content.WriteString(aw.Styles.Prompt.Render("> ") + aw.Styles.Text.Render(truncated))
			} else {
				content.WriteString(aw.Styles.System.Render("  " + truncated))
			}
			if i < min(aw.ScrollIdx+listHeight, len(aw.filtered))-1 {
				content.WriteString("\n")
			}
		}
	}

	fileBox := aw.Styles.RenderBorderedBox(content.String(), boxWidth, listBorderColor, listHeight)
	sb.WriteString(fileBox)

	// Help bar
	helpStyle := lipgloss.NewStyle().Background(aw.Styles.ColorDim).Foreground(aw.Styles.ColorMuted)
	var help string
	if aw.FilterInputFocused {
		help = "  tab: list | enter: select dir | esc: close"
	} else {
		help = "  tab: search | j/k: navigate | enter: add file | enter on dir: browse | esc: close"
	}
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render(fmt.Sprintf("%-*s", boxWidth, help)))

	return sb.String()
}

// RenderOverlay renders the attachment window on top of base content.
func (aw *AttachmentWindow) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if aw.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, aw.View().Content, screenWidth, screenHeight)
}
