package terminal

// External editor support: opening $EDITOR for multi-line input,
// display viewing, queue item editing, and file editing.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ============================================================================
// Editor Message Types
// ============================================================================

// editorFinishedMsg is sent when external editor closes (for input)
type editorFinishedMsg struct {
	content string
	err     error
}

// displayEditorFinishedMsg is sent when external editor closes (for display window viewing)
type displayEditorFinishedMsg struct {
	err error
}

// queueEditorFinishedMsg is sent when external editor closes (for queue item editing)
type queueEditorFinishedMsg struct {
	queueID string
	content string
	err     error
}

// editorStartMsg is sent to trigger actual editor execution (lazy temp file creation)
type editorStartMsg struct {
	editorCmd   string
	editorArgs  []string
	tmpFileName string
	forDisplay  bool // true if opening display window content (don't populate input)
	forQueue    bool // true if editing a queue item
	queueID     string
}

// FileEditorFinishedMsg is sent when external editor closes for a specific file
type FileEditorFinishedMsg struct {
	Path string
	Err  error
	Type string // "model_config", "runtime_config", etc. — used to decide what to reload
}

// ============================================================================
// Editor
// ============================================================================

// defaultEditors is the list of editor binaries to try when $EDITOR is not set.
// Ordered by preference per OS.
var defaultEditors []string

func init() { //nolint:gochecknoinits // platform-specific list requires init-time setup
	if runtime.GOOS == "windows" {
		defaultEditors = []string{"vim", "notepad"}
	} else {
		defaultEditors = []string{"vim", "vi"}
	}
}

// Editor handles external editor operations
type Editor struct {
	tempFilePrefix string
	content        string
}

// NewEditor creates a new editor handler
func NewEditor() *Editor {
	return &Editor{
		tempFilePrefix: "alayacore-input-*.txt",
	}
}

// Open opens an external editor for multi-line input.
// The temp file is created lazily when the command executes, not during construction.
func (e *Editor) Open(currentContent string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return editorFinishedMsg{content: "", err: fmt.Errorf("no editor found (tried: %s; set $EDITOR to override)", strings.Join(defaultEditors, ", "))}
		}
	}

	cmd, args := splitEditorCmd(editorCmd)

	// Store content for lazy temp file creation
	e.content = currentContent

	// Return a command that creates the temp file and runs the editor
	return func() tea.Msg {
		return editorStartMsg{
			editorCmd:   cmd,
			editorArgs:  args,
			tmpFileName: "", // Will be created in handleEditorStart
			forDisplay:  false,
		}
	}
}

// OpenForDisplay opens an external editor to view display window content.
// Unlike Open, this does not populate the input box when the editor closes.
func (e *Editor) OpenForDisplay(content string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return displayEditorFinishedMsg{err: fmt.Errorf("no editor found (tried: %s; set $EDITOR to override)", strings.Join(defaultEditors, ", "))}
		}
	}

	cmd, args := splitEditorCmd(editorCmd)

	// Store content for lazy temp file creation
	e.content = content

	// Return a command that creates the temp file and runs the editor
	return func() tea.Msg {
		return editorStartMsg{
			editorCmd:   cmd,
			editorArgs:  args,
			tmpFileName: "", // Will be created in handleEditorStart
			forDisplay:  true,
		}
	}
}

// OpenForQueue opens an external editor to edit a queue item's content.
// When the editor closes, the content is sent back via queueEditorFinishedMsg.
func (e *Editor) OpenForQueue(content string, queueID string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return queueEditorFinishedMsg{queueID: queueID, err: fmt.Errorf("no editor found (tried: %s; set $EDITOR to override)", strings.Join(defaultEditors, ", "))}
		}
	}

	cmd, args := splitEditorCmd(editorCmd)

	// Store content for lazy temp file creation
	e.content = content

	// Return a command that creates the temp file and runs the editor
	return func() tea.Msg {
		return editorStartMsg{
			editorCmd:   cmd,
			editorArgs:  args,
			tmpFileName: "", // Will be created in handleEditorStart
			forQueue:    true,
			queueID:     queueID,
		}
	}
}

// createTempFile creates a temp file with the editor content.
// This is called lazily when the editor is actually executed.
func (e *Editor) createTempFile() (string, error) {
	tmpFile, err := os.CreateTemp("", e.tempFilePrefix)
	if err != nil {
		return "", err
	}
	tmpFileName := tmpFile.Name()

	if e.content != "" {
		if _, err := tmpFile.WriteString(e.content); err != nil {
			tmpFile.Close()
			os.Remove(tmpFileName)
			return "", err
		}
	}
	tmpFile.Close()

	return tmpFileName, nil
}

// OpenFile opens an external editor for a specific file path.
// fileType indicates what kind of file is being edited (e.g. "model_config").
func (e *Editor) OpenFile(path, fileType string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return FileEditorFinishedMsg{Path: path, Err: fmt.Errorf("no editor found (tried: %s; set $EDITOR to override)", strings.Join(defaultEditors, ", ")), Type: fileType}
		}
	}

	cmd, args := splitEditorCmd(editorCmd)
	cmdArgs := append([]string{path}, args...)

	//nolint:gosec // G204: Editor command from user config is intentional
	return tea.ExecProcess(exec.Command(cmd, cmdArgs...), func(err error) tea.Msg {
		return FileEditorFinishedMsg{Path: path, Err: err, Type: fileType}
	})
}

// ============================================================================
// Editor Helpers
// ============================================================================

// handleEditorStart handles the lazy start of the external editor.
// This is where the temp file is actually created, ensuring cleanup happens properly.
func (m *Terminal) handleEditorStart(msg editorStartMsg) (tea.Model, tea.Cmd) {
	// Create temp file lazily
	tmpFileName, err := m.editor.createTempFile()
	if err != nil {
		m.out.AppendError("Failed to create temp file: %v", err)
		return m, nil
	}

	cmdArgs := append([]string{tmpFileName}, msg.editorArgs...)
	//nolint:gosec // G204: Editor command from user config is intentional
	cmd := exec.Command(msg.editorCmd, cmdArgs...)

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(tmpFileName)

		if err != nil {
			if msg.forDisplay {
				return displayEditorFinishedMsg{err: err}
			}
			if msg.forQueue {
				return queueEditorFinishedMsg{queueID: msg.queueID, err: err}
			}
			return editorFinishedMsg{content: "", err: err}
		}

		// For display viewing, we don't need to read the content back
		if msg.forDisplay {
			return displayEditorFinishedMsg{err: nil}
		}

		content, readErr := os.ReadFile(tmpFileName)
		if readErr != nil {
			if msg.forQueue {
				return queueEditorFinishedMsg{queueID: msg.queueID, err: readErr}
			}
			return editorFinishedMsg{content: "", err: readErr}
		}

		if msg.forQueue {
			return queueEditorFinishedMsg{queueID: msg.queueID, content: string(content), err: nil}
		}

		return editorFinishedMsg{content: string(content), err: nil}
	})
}

// FormatEditorContent formats editor content for preview in the input field
func FormatEditorContent(content string) string {
	lineCount := strings.Count(content, "\n") + 1

	// For single-line content, show it just like regular user input (no suffix)
	if lineCount == 1 {
		return content
	}

	// For multi-line content, show summary with line count and preview
	preview := strings.Fields(content)
	var previewText string
	switch {
	case len(preview) > 0 && len(preview[0]) > 20:
		previewText = preview[0][:20] + "..."
	case len(preview) > 0:
		previewText = preview[0]
	default:
		previewText = "(empty)"
	}
	return fmt.Sprintf("[%d lines] %s (press Enter to send)", lineCount, previewText)
}

// getEditorCommand returns the editor command to use
func getEditorCommand(editorCmd string) string {
	if editorCmd != "" {
		return editorCmd
	}

	for _, editor := range defaultEditors {
		path, err := exec.LookPath(editor)
		if err == nil {
			return path
		}
	}

	return ""
}

// splitEditorCmd splits an editor command string into the executable and its arguments.
// For example, "code --wait" becomes ("code", ["--wait"]).
func splitEditorCmd(editorCmd string) (string, []string) {
	parts := strings.Fields(editorCmd)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

// hasEditorPrefix checks if the value has an editor content prefix.
func hasEditorPrefix(value string) bool {
	return len(value) > 0 && value[0] == '['
}
