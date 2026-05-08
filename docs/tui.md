# Terminal UI

AlayaCore's terminal UI is built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), and [Lip Gloss](https://github.com/charmbracelet/lipgloss) and uses vim-like keybindings throughout.

## Navigation

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `j` | Move window cursor down |
| `k` | Move window cursor up |
| `J` / `Shift+Down` | Scroll down one line |
| `K` / `Shift+Up` | Scroll up one line |
| `Ctrl+D` | Scroll down half screen |
| `Ctrl+U` | Scroll up half screen |
| `g` | Go to first window, scroll to top |
| `G` | Go to last window, enable follow |
| `H` | Move cursor to top window in visible area |
| `M` | Move cursor to middle window in visible area |
| `L` | Move cursor to bottom window in visible area |
| `f` | Jump to next user prompt |
| `b` | Jump to previous user prompt |
| `e` | Open window content in external editor |

## Input & Actions

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt |
| `Ctrl+S` | Save session |
| `Ctrl+O` | Open external editor (`$EDITOR`) for multi-line input |
| `Ctrl+L` | Open model selector |
| `Ctrl+P` | Open theme selector |
| `Ctrl+Q` | Open task queue manager |
| `Ctrl+H` | Open help window |
| `Ctrl+G` | Cancel current request (with confirmation) |
| `Ctrl+C` | Clear input field (only when input is focused) |
| `:` | Switch to input with `:` prefix (command mode) |
| `Space` | Toggle window fold (expand/collapse) |

## Session Commands

Commands are split into two categories:

**Immediate commands** — run immediately, no task running required:
| Command | Action |
|---------|--------|
| `:cancel` | Cancel current request (with confirmation) |
| `:cancel_all` | Cancel current request and clear the task queue |
| `:model_set <id>` | Switch to a model by numeric ID |
| `:model_load` | Reload model configs from the config file |
| `:think [0\|1\|2]` | Set think level (0=off, 1=normal, 2=max). Default: 1 |
| `:taskqueue_get_all` | List all queued tasks (used by queue manager UI) |
| `:taskqueue_del <id>` | Delete a queued task by ID (used by queue manager UI) |
| `:taskqueue_edit <id> <content>` | Edit a queued task's content by ID (used by queue manager UI) |

**Deferred commands** — enqueued at the front of the task queue; require no task currently running:
| Command | Action |
|---------|--------|
| `:continue [skip]` | Resend the last prompt, or skip it with `skip` and resume the queue |
| `:summarize` | Summarize conversation to reduce token usage |
| `:save [filename]` | Save session. Uses `--session` path if no filename given. |

Note: `:quit` / `:q` and `:help` are handled directly by each adaptor (terminal shows a confirmation dialog for quit, opens help window for help; plainio exits immediately for quit) and never reaches the session command dispatch.

## Window Container

The display area organizes content into separate windows — one per message or tool call. Windows have synchronized widths and can be navigated independently.

### Tool Result Separator

`write_file` and `edit_file` windows insert a dimmed `───` separator between the tool call (showing the file path) and the tool result. This visually separates the content-heavy input from the output. Other tool windows (e.g. `read_file`, `execute_command`) don't use a separator — their call header is short and the result follows directly.

### Auto-Follow

When new windows appear, the cursor automatically moves to the newest one. Pressing `k`, `g`, `H`, `M`, `K`, `Ctrl+D`, `Ctrl+U`, `e`, `f`, or `b` disables auto-follow. Pressing `G` (go to last window) re-enables it.

### Fold Mode

Press `Space` on any window to collapse it — the window shows the first line, a fold indicator, and the last 3 lines. Press `Space` again to expand.

### Virtual Scrolling

The display uses virtual scrolling to handle large outputs efficiently. Only visible windows are rendered, giving a 3.5x speedup over naive rendering. See [virtual-rendering-performance.md](virtual-rendering-performance.md) for details.

## Task Queue Manager

When you submit prompts or commands while a previous task is running, they are queued. Press `Ctrl+Q` to manage the queue:

| Key | Action |
|-----|--------|
| `q`, `Esc` | Close queue manager |
| `j`, `↓` | Move selection down |
| `k`, `↑` | Move selection up |
| `d` | Delete selected task |
| `e` | Edit selected task in external editor |

Each queued task shows its queue ID (Q1, Q2, …), type (`P` for prompt, `C` for command), and a truncated content preview.

## Help Window

Press `Ctrl+H` or type `:help` to open a help window listing all keybindings and commands. The filter input at the top lets you fuzzy-search for specific keys or commands (e.g. typing `gt` matches `:taskqueue_get_all`):

| Key | Action |
|-----|--------|
| `Tab` | Toggle focus between filter input and list |
| `q`, `Esc` | Close help window |
| `j`, `↓` | Move selection down |
| `k`, `↑` | Move selection up |
| `Enter` | Copy selected command to input (commands only) |

The help window is organized into three sections:

- **Commands** — colon commands available in the input field (queue manager internals like `:taskqueue_get_all` and `:taskqueue_del` are omitted)
- **Global Shortcuts** — keybindings that work from any context
- **Display Mode** — navigation and editing keys for the display area

The help window uses the same size, position, and overlay pattern as the task queue manager and theme selector.
