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
// Editor Action Types
// ============================================================================

// EditorAction classifies what should happen after the editor closes.
type EditorAction int

const (
	EditorActionNone         EditorAction = iota // e.g. display viewing — no side effects
	EditorActionSubmit                           // submit content as user input
	EditorActionReloadConfig                     // reload model/runtime config after edit
)

// EditorFinishedMsg is sent when an external editor closes.
//
// - For EditorActionNone: content is empty, no side effects.
// - For EditorActionSubmit: content contains the editor's text.
// - For EditorActionReloadConfig: Path + FileType identify the edited file.
type EditorFinishedMsg struct {
	Content  string
	Err      error
	Action   EditorAction
	Path     string // for EditorActionReloadConfig
	FileType string // for EditorActionReloadConfig ("model_config", etc.)
}

// editorStartMsg is sent to trigger actual editor execution (lazy temp file creation)
type editorStartMsg struct {
	editorCmd  string
	editorArgs []string
	action     EditorAction
	path       string
	fileType   string
}

// ============================================================================
// Editor Message Helpers
// ============================================================================

// errEditorNotFound is returned when no text editor binary is found.
func errEditorNotFound() error {
	return fmt.Errorf("no editor found (tried: %s; set $EDITOR to override)", strings.Join(defaultEditors, ", "))
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
func (e *Editor) Open(currentContent string) tea.Cmd {
	return e.openWithAction(currentContent, EditorActionSubmit, "", "")
}

// OpenForDisplay opens an external editor to view display window content.
// Content is NOT read back — this is a view-only operation.
func (e *Editor) OpenForDisplay(content string) tea.Cmd {
	return e.openWithAction(content, EditorActionNone, "", "")
}

// OpenFile opens an external editor for a specific file path.
func (e *Editor) OpenFile(path, fileType string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{
				Err:      errEditorNotFound(),
				Action:   EditorActionReloadConfig,
				Path:     path,
				FileType: fileType,
			}
		}
	}

	cmd, args := splitEditorCmd(editorCmd)
	cmdArgs := append([]string{path}, args...)

	//nolint:gosec // G204: Editor command from user config is intentional
	return tea.ExecProcess(exec.Command(cmd, cmdArgs...), func(err error) tea.Msg {
		return EditorFinishedMsg{
			Err:      err,
			Action:   EditorActionReloadConfig,
			Path:     path,
			FileType: fileType,
		}
	})
}

// openWithAction is the shared implementation for all editor operations
// that require temp file creation (input, display viewing).
func (e *Editor) openWithAction(content string, action EditorAction, path, fileType string) tea.Cmd {
	editorCmd := getEditorCommand(os.Getenv("EDITOR"))

	if editorCmd == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{Err: errEditorNotFound(), Action: action, Path: path, FileType: fileType}
		}
	}

	cmd, args := splitEditorCmd(editorCmd)

	// Store content for lazy temp file creation
	e.content = content

	return func() tea.Msg {
		return editorStartMsg{
			editorCmd:  cmd,
			editorArgs: args,
			action:     action,
			path:       path,
			fileType:   fileType,
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

// ============================================================================
// Editor Helpers
// ============================================================================

// handleEditorStart handles the lazy start of the external editor.
// Temp file is created here, ensuring cleanup happens properly.
func (m *Terminal) handleEditorStart(msg editorStartMsg) (tea.Model, tea.Cmd) {
	tmpFileName, err := m.editor.createTempFile()
	if err != nil {
		m.out.WriteError("Failed to create temp file: %v", err)
		return m, nil
	}

	cmdArgs := append([]string{tmpFileName}, msg.editorArgs...)
	//nolint:gosec // G204: Editor command from user config is intentional
	cmd := exec.Command(msg.editorCmd, cmdArgs...)

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(tmpFileName)

		// Build the result message with common fields
		result := EditorFinishedMsg{
			Err:      err,
			Action:   msg.action,
			Path:     msg.path,
			FileType: msg.fileType,
		}

		if err != nil {
			return result
		}

		// For view-only (display), don't read content back
		if msg.action == EditorActionNone {
			return result
		}

		content, readErr := os.ReadFile(tmpFileName)
		if readErr != nil {
			result.Err = readErr
			return result
		}

		result.Content = string(content)
		return result
	})
}

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
