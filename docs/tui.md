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
| `Ctrl+Z` | Suspend process |
| `Ctrl+C` | Clear input field (only when input is focused) |
| `:` | Switch to input with `:` prefix (command mode) |
| `Space` | Toggle window fold (expand/collapse) |

## Session Commands

Commands are split into two categories:

**Immediate commands** — run synchronously in the main event loop, no queuing:
| Command | Action |
|---------|--------|
| `:cancel` | Cancel current request (with confirmation) |
| `:cancel_all` | Cancel current request and clear the task queue |
| `:save [filename]` | Save session. Uses `--session` path if no filename given. |
| `:model_set <id>` | Switch to a model by numeric ID |
| `:model_load` | Reload model configs from the config file |
| `:theme_set <name>` | Switch to a different theme (applied visually via session broadcast) |
| `:reason [0\|1\|2]` | Set reasoning level (0=off, 1=normal, 2=max). Default: 1 |
| `:suspend` | Suspend the process (Ctrl+Z) |
| `:taskqueue_get_all` | List all queued tasks (used by queue manager UI) |
| `:taskqueue_del <id>` | Delete a queued task by ID (used by queue manager UI) |
| `:taskqueue_edit <id> <content>` | Edit a queued task's content by ID (used by queue manager UI) |

**Deferred commands** — enqueued at the front of the task queue; run in a task goroutine when no task is running. They can be canceled with `:cancel` while executing:
| Command | Action |
|---------|--------|
| `:continue [skip]` | Resend the last prompt, or skip it with `skip` and resume the queue |
| `:summarize` | Summarize conversation to reduce token usage ⚠️ **Replaces entire conversation history with a summary** — see [context-tracking.md](context-tracking.md) |

Note: `:quit` / `:q`, `:help`, and `:suspend` are handled directly by each adaptor (terminal shows a confirmation dialog for quit, opens help window for help, suspends the process for suspend; plainio exits immediately for quit and help, and does not support suspend; rawio passes all commands through to the session since it doesn't interpret frame payloads) and never reaches the session command dispatch.

## Window Container

The display area organizes content into separate windows — one per message or tool call. Windows have synchronized widths and can be navigated independently.

### Tool Result Separator

`write_file` and `edit_file` windows insert a dimmed `OUTPUT:` label line between the tool call (showing the file path) and the tool result. This visually separates the content-heavy input from the output. Other tool windows (e.g. `read_file`, `execute_command`) don't use a separator — their call header is short and the result follows directly.

### Auto-Follow

Auto-follow is enabled by default at startup. When enabled, the viewport
automatically scrolls to keep the newest content visible as it arrives.

Auto-follow is disabled by any navigation that actually moves the cursor or
scrolls the viewport. While auto-follow is active:

| Key | Behavior | Disables auto-follow? |
|-----|----------|-----------------------|
| `G` | Go to last window | ✅ Re-enables |
| `j` / `↓` | Move cursor down | ❌ No-op (race protection) |
| `L` | Move cursor to bottom | ❌ No-op (race protection) |
| `J` / `Shift+Down` | Scroll down one line | ❌ No-op when at bottom |
| `Ctrl+D` | Scroll down half screen | ❌ No-op when at bottom |
| `k` / `↑` | Move cursor up | ✅ If cursor actually moves |
| `H` | Move to top of visible area | ✅ If cursor actually moves |
| `M` | Move to center of visible area | ✅ If cursor actually moves |
| `f` | Jump to next user prompt | ✅ If cursor actually moves |
| `b` | Jump to previous user prompt | ✅ If cursor actually moves |
| `g` / `Home` | Go to first window | ✅ If cursor actually moves |
| `K` / `Shift+Up` | Scroll up one line | ✅ Always |
| `Ctrl+U` | Scroll up half screen | ✅ Always |
| `e` | Open in editor | ✅ Always |
| `Space` | Toggle window fold | ❌ Never |
| `Tab` | Toggle focus | ❌ Never |

### Fold Mode

Press `Space` on any window to collapse it — the window shows the first 2 lines, a centered fold indicator, and the last 2 lines. Press `Space` again to expand.

### Virtual Scrolling

The display uses virtual scrolling to handle large outputs efficiently. Only visible windows are rendered, giving a 3.5x speedup over naive rendering. See [virtual-rendering-performance.md](virtual-rendering-performance.md) for details.

### Sentinel values

`WindowBuffer.dirtyIndex` uses a sentinel (`dirtyFullRebuild = -2`) to signal that all windows need recalculation. State transitions must check whether the sentinel is already set before overwriting — an `else` branch that blindly assigns a new index can downgrade a full-rebuild to a single-window update, silently dropping windows from the display. See `window.go` → `markDirty`.

### ANSI escape sequences are not recursive

When styling text with lipgloss, each segment must be rendered individually before concatenation. You cannot render a string that already contains ANSI codes with a new style and expect it to work.

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

## Line Wrapping

Content in each window is soft-wrapped to fit the available width. The wrapping is **character-boundary** — it breaks mid-word when a word exceeds the line width, matching how a typical terminal behaves.

Width calculation is **Unicode-aware**:

- ASCII / Latin characters occupy **1 cell**
- CJK characters (中文、日本語、한국어) occupy **2 cells**
- Emoji occupy **2 cells** (grapheme clusters per Unicode UAX #29)
- ANSI escape codes (colors, bold, etc.) occupy **0 cells**

When a newline is inserted by the wrapper, ANSI styles are automatically carried forward to the next line — a red-styled sentence stays red on continuation lines.

Incremental updates avoid re-wrapping the entire content on every token. Only the last line is combined with the new delta and re-wrapped, keeping per-token cost proportional to the delta size rather than total content.

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
