package terminal

// AttachmentWindow is a file picker overlay for adding attachments to user input.
// It provides two modes:
//   - Local mode:  path input field with a filtered file list, similar to ModelSelector.
//   - URL mode:    URL input field for adding remote attachments.
// Users can toggle between modes with Ctrl+A.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type attachmentMode int

const (
	modeLocal attachmentMode = iota
	modeURL
)

// AttachmentWindow manages file/URL selection for multi-modal input.
type AttachmentWindow struct {
	FilteredListCore

	entries    []fileEntry
	filtered   []fileEntry
	currentDir string // actual directory being displayed
	baseDir    string // anchor for resolving relative path inputs (only updated on explicit navigation)
	mode       attachmentMode

	// Callback: invoked when user adds a file or URL.
	onAdd func(path string)
}

// fileEntry represents a single file system entry.
type fileEntry struct {
	name      string
	nameLower string // pre-computed lowercase of name, for filtering
	isDir     bool
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

// SetOnAdd sets the callback for when a file or URL is selected.
func (aw *AttachmentWindow) SetOnAdd(fn func(path string)) {
	aw.onAdd = fn
}

// Open opens the attachment window and loads the current directory.
func (aw *AttachmentWindow) Open() {
	aw.State = FilteredListOpen
	aw.mode = modeLocal
	aw.FilterInput.SetValue("")
	aw.FilterInput.Prompt = "F "
	aw.lastFilterValue = "\x00"
	aw.FilterInputFocused = true
	aw.FilterInput.Focus()
	aw.updateFilterInputStyles()
	aw.ScrollIdx = 0
	aw.SelectedIdx = 0
	aw.currentDir, _ = os.Getwd()
	aw.baseDir = aw.currentDir
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
	if dir != "/" {
		result = append(result, fileEntry{name: "..", nameLower: "..", isDir: true})
	}
	for _, e := range entries {
		name := e.Name()
		result = append(result, fileEntry{
			name:      name,
			nameLower: strings.ToLower(name),
			isDir:     e.IsDir(),
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

	// Ctrl+A toggles between local and URL mode
	if key == keyCtrlA {
		aw.toggleMode()
		return nil
	}

	// URL mode: Enter submits the URL immediately
	if aw.mode == modeURL && key == keyEnter {
		aw.handleURLEntry()
		return nil
	}

	// Capture focus state before processing — handleEnter for a directory
	// switches focus to the filter input, and we need to avoid double-processing
	// when handleLocalModeKeys runs afterwards.
	inputWasFocused := aw.FilterInputFocused

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
		if aw.mode == modeLocal {
			aw.handleLocalModeKeys(filterChanged, key, inputWasFocused)
		}
		return cmd
	}

	if aw.mode == modeLocal && !aw.FilterInputFocused {
		aw.handleListKeys(key)
	}

	return nil
}

func (aw *AttachmentWindow) handleLocalModeKeys(filterChanged bool, key string, inputWasFocused bool) {
	if filterChanged && aw.FilterInputFocused {
		// Ctrl+C clears the filter — also reset directory to baseDir
		if key == keyCtrlC {
			aw.currentDir = aw.baseDir
			aw.loadDir(aw.baseDir)
		}
		aw.updateFiltered()
	}
	if !aw.FilterInputFocused {
		aw.handleListKeys(key)
	}
	// Only handle Enter if the filter input was already focused before this key
	// event.  When the list handles Enter on a directory it switches focus to
	// the filter — we must not double-process the same key.
	if aw.FilterInputFocused && key == keyEnter && len(aw.filtered) > 0 && inputWasFocused {
		aw.handleSearchEnter()
	}
}

func (aw *AttachmentWindow) toggleMode() {
	aw.FilterInput.SetValue("")
	aw.lastFilterValue = "\x00"
	if aw.mode == modeLocal {
		aw.mode = modeURL
		aw.FilterInput.Prompt = "U "
		aw.FilterInput.Focus()
		aw.FilterInputFocused = true
		aw.updateFilterInputStyles()
	} else {
		aw.mode = modeLocal
		aw.FilterInput.Prompt = "F "
		aw.loadDir(aw.currentDir)
		aw.FilterInput.Focus()
		aw.FilterInputFocused = true
		aw.updateFilterInputStyles()
	}
}

func (aw *AttachmentWindow) handleURLEntry() bool {
	url := strings.TrimSpace(aw.FilterInput.Value())
	if url == "" {
		return false
	}
	if aw.onAdd != nil {
		aw.onAdd(url)
	}
	aw.State = FilteredListClosed
	return true
}

// handlePaste handles paste events in the attachment window.
// It updates the input field and, in local mode, triggers directory
// navigation and file list filtering — the same update path that
// keyboard input follows via HandleKeyMsg.
func (aw *AttachmentWindow) handlePaste(msg tea.PasteMsg) {
	if !aw.FilterInputFocused {
		return
	}
	aw.FilterInput.Update(msg)
	if aw.mode == modeLocal {
		aw.updateFiltered()
	}
}

// autocompleteDir replaces the last path segment in the filter input with the
// given directory name (plus trailing "/"), then re-filters to trigger
// navigateByPath.
func (aw *AttachmentWindow) autocompleteDir(dirName string) {
	search := aw.FilterInput.Value()
	switch {
	case strings.Contains(search, "/"):
		prefix := search[:strings.LastIndex(search, "/")+1]
		aw.FilterInput.SetValue(prefix + dirName + "/")
	case strings.HasPrefix(search, "~"):
		aw.FilterInput.SetValue("~/" + dirName + "/")
	default:
		aw.FilterInput.SetValue(dirName + "/")
	}
	aw.lastFilterValue = "\x00"
	aw.updateFiltered()
}

func (aw *AttachmentWindow) handleEnter() bool {
	// Enter when the list is focused.
	if len(aw.filtered) == 0 || aw.SelectedIdx < 0 || aw.SelectedIdx >= len(aw.filtered) {
		return false
	}
	entry := aw.filtered[aw.SelectedIdx]
	if entry.isDir {
		// Switch to input mode and autocomplete the directory name.
		aw.FilterInputFocused = true
		aw.FilterInput.Focus()
		aw.updateFilterInputStyles()
		aw.autocompleteDir(entry.name)
		return true
	}
	// File → add it
	fullPath := filepath.Join(aw.currentDir, entry.name)
	if aw.onAdd != nil {
		aw.onAdd(fullPath)
	}
	aw.State = FilteredListClosed
	return true
}

func (aw *AttachmentWindow) handleSearchEnter() {
	// Enter when the filter input is focused.
	if len(aw.filtered) == 0 || aw.SelectedIdx < 0 || aw.SelectedIdx >= len(aw.filtered) {
		return
	}
	entry := aw.filtered[aw.SelectedIdx]
	if entry.isDir {
		aw.autocompleteDir(entry.name)
		return
	}
	// File → add it
	fullPath := filepath.Join(aw.currentDir, entry.name)
	if aw.onAdd != nil {
		aw.onAdd(fullPath)
	}
	aw.State = FilteredListClosed
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

// navigateByPath treats the input as a path and navigates accordingly.
//
// Rule: the last "/"-separated segment is the file filter, everything before it is the directory.
// If input ends with "/", the whole path is the directory (show all files).
//
// Three kinds of paths:
//
//	Absolute (starts with "/"):      /etc       → dir="/", file="etc"
//	                                 /etc/ssl   → dir="/etc", file="ssl"
//	Home-relative (starts with "~"): ~/.ala     → dir="~", file=".ala"
//	                                 ~/.ala/    → dir="~/.ala"
//	Relative (contains "/" or ".."): subdir/    → dir="./subdir"
//	                                 subdir/f   → dir="./subdir", file="f"
//	                                 ..         → dir=".."
func (aw *AttachmentWindow) navigateByPath(search string) string {
	var absDir, filter string

	switch {
	case strings.HasPrefix(search, "~"):
		home, err := os.UserHomeDir()
		if err != nil {
			return search
		}
		if search == "~" {
			// "~" alone → home, show all
			aw.currentDir = home
			aw.loadDir(home)
			aw.lastFilterValue = "\x00"
			return ""
		}
		rest := search[1:] // everything after "~"
		if strings.HasSuffix(rest, "/") {
			absDir = filepath.Join(home, rest)
			filter = ""
		} else {
			absDir = filepath.Join(home, filepath.Dir(rest))
			filter = filepath.Base(rest)
		}

	case strings.HasPrefix(search, "/"):
		if strings.HasSuffix(search, "/") {
			absDir = search
			filter = ""
		} else {
			absDir = filepath.Dir(search)
			filter = filepath.Base(search)
		}

	case strings.Contains(search, "/") || search == "..":
		// Relative path — resolve from baseDir (fixed anchor)
		switch {
		case search == "..":
			absDir = filepath.Join(aw.baseDir, "..")
			filter = ""
		case strings.HasSuffix(search, "/"):
			absDir = filepath.Join(aw.baseDir, search)
			filter = ""
		default:
			absDir = filepath.Join(aw.baseDir, filepath.Dir(search))
			filter = filepath.Base(search)
		}

	default:
		return search // normal name filtering
	}

	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		return search // directory doesn't exist, keep filter as-is
	}

	aw.currentDir = absDir
	aw.loadDir(absDir)
	aw.lastFilterValue = "\x00"
	return filter
}

// updateFiltered rebuilds the filtered file list based on filter input.
func (aw *AttachmentWindow) updateFiltered() {
	search := aw.FilterInput.Value()
	if search == aw.lastFilterValue {
		return
	}

	var prevSelectedIdx int
	if aw.SelectedIdx >= 0 && aw.SelectedIdx < len(aw.filtered) {
		prevSelectedIdx = aw.SelectedIdx
	}

	aw.lastFilterValue = search

	// Try absolute path navigation for "/" and "~" prefixes
	search = aw.navigateByPath(search)

	if search == "" {
		aw.filtered = make([]fileEntry, len(aw.entries))
		copy(aw.filtered, aw.entries)
	} else {
		term := strings.ToLower(search)
		aw.filtered = aw.filtered[:0]
		for _, e := range aw.entries {
			if FuzzyMatch(term, e.nameLower) {
				aw.filtered = append(aw.filtered, e)
			}
		}
	}

	if prevSelectedIdx >= 0 && prevSelectedIdx < len(aw.filtered) {
		aw.SelectedIdx = prevSelectedIdx
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

	titleStyle := lipgloss.NewStyle().Background(aw.Styles.ColorDim).Foreground(aw.Styles.ColorAccent).Bold(true)
	sb.WriteString(titleStyle.Render(fmt.Sprintf("%-*s", aw.Width, "  Attachments")))
	sb.WriteString("\n")

	// Mode indicator
	modeText := "Local"
	if aw.mode == modeURL {
		modeText = "URL"
	}
	sb.WriteString(aw.Styles.System.Render(fmt.Sprintf("  Mode: %s  (Ctrl+A to switch)", modeText)))
	sb.WriteString("\n")

	// Search / URL input box
	searchBox := aw.Styles.RenderBorderedBox(aw.FilterInput.View(), aw.Width, aw.FilterBorderColor())
	sb.WriteString(searchBox)
	sb.WriteString("\n")

	boxWidth := lipgloss.Width(searchBox)

	if aw.mode == modeURL {
		aw.renderURLBody(&sb)
	} else {
		aw.renderLocalBody(&sb, boxWidth)
	}

	// Help bar
	helpStyle := lipgloss.NewStyle().Background(aw.Styles.ColorDim).Foreground(aw.Styles.ColorMuted)
	var help string
	switch {
	case aw.mode == modeURL:
		help = "  enter: add URL | ctrl+a: switch to local | esc: close"
	case aw.FilterInputFocused:
		help = "  tab: list | enter: select dir | ctrl+a: switch to URL | esc: close"
	default:
		help = "  tab: search | j/k: navigate | enter: add file | enter on dir: browse | ctrl+a: switch to URL | esc: close"
	}
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render(fmt.Sprintf("%-*s", boxWidth, help)))

	return sb.String()
}

// renderURLBody renders the URL mode body — just a hint line.
func (aw *AttachmentWindow) renderURLBody(sb *strings.Builder) {
	sb.WriteString(aw.Styles.System.Render("  Enter a URL to attach (e.g. https://example.com/image.jpg)"))
	sb.WriteString("\n")
}

// renderLocalBody renders the local file browser body — directory path and file list.
func (aw *AttachmentWindow) renderLocalBody(sb *strings.Builder, boxWidth int) {
	sb.WriteString(aw.Styles.System.Render(aw.currentDir))
	sb.WriteString("\n")

	listBorderColor := aw.ListBorderColor()
	listHeight := SelectorListRows
	innerWidth := max(0, boxWidth-BorderInnerPadding)

	var content strings.Builder
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

	fileBox := aw.Styles.RenderBorderedBox(content.String(), boxWidth, listBorderColor, listHeight)
	sb.WriteString(fileBox)
}

// RenderOverlay renders the attachment window on top of base content.
func (aw *AttachmentWindow) RenderOverlay(baseContent string, screenWidth, screenHeight int) string {
	if aw.State == FilteredListClosed {
		return baseContent
	}
	return renderOverlay(baseContent, aw.View().Content, screenWidth, screenHeight)
}
