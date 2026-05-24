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
| `:theme_set <name>` | Set the active theme |
| `:think [0\|1\|2]` | Set think level (0=off, 1=normal, 2=max). Default: 1 |
| `:suspend` | Suspend the process (Ctrl+Z) |
| `:taskqueue_get_all` | List all queued tasks (used by queue manager UI) |
| `:taskqueue_del <id>` | Delete a queued task by ID (used by queue manager UI) |
| `:taskqueue_edit <id> <content>` | Edit a queued task's content by ID (used by queue manager UI) |

**Deferred commands** — enqueued at the front of the task queue; run in a task goroutine when no task is running. They can be canceled with `:cancel` while executing:
| Command | Action |
|---------|--------|
| `:continue [skip]` | Resend the last prompt, or skip it with `skip` and resume the queue |
| `:summarize` | Summarize conversation to reduce token usage ⚠️ **Replaces entire conversation history with a single summary message** — see [context-tracking.md](context-tracking.md) |

Note: `:quit` / `:q`, `:help`, and `:suspend` are handled directly by each adaptor (terminal shows a confirmation dialog for quit, opens help window for help, suspends the process for suspend; plainio exits immediately for quit and help, and does not support suspend) and never reaches the session command dispatch.

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

### How It Works

Content in each window is soft-wrapped to fit the available width. The wrapping is **character-boundary** — it breaks mid-word when a word exceeds the line width, rather than waiting for a word boundary. This matches how a typical terminal behaves.

Width calculation is **Unicode-aware**:

- ASCII / Latin characters occupy **1 cell**
- CJK characters (中文、日本語、한국어) occupy **2 cells**
- Emoji occupy **2 cells** (the width calculation operates on grapheme clusters per Unicode UAX #29, so combining marks and ZWJ sequences are resolved as part of their parent cluster)
- ANSI escape codes (colors, bold, etc.) occupy **0 cells** — they are invisible to the width calculation

When a newline is inserted by the wrapper, ANSI styles are automatically carried forward to the next line. For example, if a red-styled sentence wraps, the continuation line stays red — you don't see style resets at wrap points.

### How It's Implemented

All wrapping flows through a single function, `wrapContent(s string, width int) string`. There are no duplicate wrapping paths.

```
wrapContent(s, width)
  ├── Step 1: ansi.Hardwrap(s, width, false)
  │     Inserts \n at the correct position by counting display
  │     width of each grapheme cluster. Uses the charmbracelet/x/ansi
  │     library which delegates to clipperhouse/displaywidth for
  │     Unicode character width lookup (EastAsianWidth table).
  │
  └── Step 2: lipgloss.NewWrapWriter(buf)
        Re-applies ANSI styles after each inserted \n.
        Without this, terminals would reset colors/styles at
        line breaks. The WrapWriter tracks the current style
        state (SGR attributes + hyperlink) and re-emits it
        on the new line.
```

**Incremental updates** avoid re-wrapping the entire content on every token. When `AppendContent(delta)` is called:

1. The delta is styled (tag-based colors applied)
2. The last cached line is concatenated with the styled delta
3. Only that combined line is re-wrapped via `wrapContent`
4. The result replaces the old last line in the cache

This keeps per-token cost at O(delta) instead of O(total content), which is critical for streaming performance.

#### Key Point: `lastLine + delta`

```go
// appendDeltaToLines core logic
lastLine := lines[len(lines)-1]
combined := lastLine + delta
newLines := wrapLines(combined, width)
return append(lines[:len(lines)-1], newLines...)
```

`lastLine` is the product of the previous wrap and already carries the full ANSI style state (e.g. `\x1b[31m...`). After concatenating `delta`, the resulting string has a continuous ANSI state. When fed to `wrapContent`:

1. `Hardwrap` inserts `\n` at the correct display width
2. `WrapWriter` resets and re-emits styles after each `\n`

**Styles are never lost during incremental appends** — not because there is special style-preservation logic, but because `lastLine` already holds the complete ANSI context, and `wrapContent` handles cross-line style repair.

**Full rebuild** happens when the window width changes or the cache is invalidated (e.g. style change, fold toggle). The full content is prepared (strip input ANSI, expand tabs), styled, and wrapped from scratch.

**Callers that invoke `wrapContent`:**

| Caller | Context |
|--------|---------|
| `renderGenericContent` | Full render of generic (non-diff) content |
| `appendDeltaToLines` → `wrapLines` | Incremental streaming updates |
| `RenderDiffContent` | Per-line wrapping of diff hunks (styled before wrapping) |
| `rebuildCache` (tool result path) | Wraps tool result section |

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
