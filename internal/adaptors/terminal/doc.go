// Package terminal provides the terminal user interface for AlayaCore.
//
// The terminal package implements a Bubble Tea-based TUI that serves as the
// primary interface for interacting with the AI assistant. It handles:
//
//   - User input (text prompts and commands)
//   - Display of assistant responses with styling
//   - Model selection and switching
//   - Task queue management
//   - Focus management between input and display windows
//
// Architecture Overview:
//
//	The terminal UI follows the Bubble Tea architecture (Elm-style):
//	  - Terminal: The main model that composes all components
//	  - DisplayModel: Renders assistant output with virtual scrolling
//	  - InputModel: Handles user text input with external editor support
//	  - Status bar: Shows session status (tokens, queue, model info)
//	  - ModelSelector: Modal for switching between AI models
//	  - QueueManager: Modal for managing the task queue
//
// Communication with the session layer uses TLV (Tag-Length-Value) protocol:
//   - Input: io.WriteCloser sends TLV messages to the session
//   - Output: OutputWriter parses TLV and renders styled content
//
// Key Files:
//
//   - terminal.go: Main Terminal model, message routing, and status bar
//   - keybinds.go: Declarative key binding configuration
//   - output.go: TLV parsing and styled rendering
//   - window.go: Virtual scrolling, DisplayModel, and diff display
//   - styles.go: Lipgloss style derivation from theme.Theme
//   - input_component.go: Input handling and external editor support
//   - model_selector.go: Model switching UI with fuzzy search
//   - queue_manager.go: Task queue UI
//   - theme_manager.go: Wrapper around theme.Manager with startup warnings
//   - theme_selector.go: Theme selection UI with live preview
//   - warnings.go: Warning collection for non-fatal initialization errors
//   - overlay.go: Overlay rendering for selectors and queue manager
//   - help_window.go: Keybinding and command help overlay
//   - confirm_dialog.go: Confirmation dialogs for quit/cancel/tool
//   - tool.go, tool_handler.go: Tool execution display
//
// Theme data types (Theme struct, DefaultTheme, LoadTheme) and the core
// Manager live in internal/theme — shared with future GUI adaptors.
//
// Usage:
//
//	terminal := NewTerminal(output, input, config, width, height)
//	p := tea.NewProgram(terminal)
//	p.Run()
package terminal
