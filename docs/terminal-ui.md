# Terminal UI

## Navigation

| Key | Action |
|-----|--------|
| `Tab` | Switch focus between display and input window |
| `j` | Move window cursor down (when display focused) |
| `k` | Move window cursor up (when display focused) |
| `J` | Scroll down one line (when display focused) |
| `K` | Scroll up one line (when display focused) |
| `g` | Go to first window and top of display (when display focused) |
| `G` | Go to last window and bottom of display (when display focused) |
| `H` | Move cursor to window at top of visible area (when display focused) |
| `L` | Move cursor to window at bottom of visible area (when display focused) |
| `M` | Move cursor to window at center of visible area (when display focused) |

## Input & Actions

| Key | Action |
|-----|--------|
| `Enter` | Submit prompt (when input focused) |
| `Ctrl+S` | Save session to file |
| `Ctrl+O` | Open external editor for multi-line input |
| `Ctrl+L` | Open model selector UI |
| `Ctrl+P` | Open theme selector UI |
| `Ctrl+Q` | Open task queue manager UI |
| `:` | Switch to input with ":" prefix (when display focused) |
| `Space` | Toggle window fold (expand/collapse) (when display focused) |
| `Ctrl+C` | Clear input (when input focused) |
| `Ctrl+G` | Cancel current request (with confirmation) |

## Session Commands

| Command | Action |
|---------|--------|
| `:save [filename]` | Save session to file (uses `--session` path if no filename) |
| `:cancel` | Cancel current request (with confirmation) |
| `:cancel_all` | Cancel current request and clear the task queue |
| `:retry` | Retry the last prompt (re-send history; appends "Please continue." if latest message is from the assistant) |
| `:summarize` | Summarize conversation to reduce token usage |
| `:quit`, `:q` | Exit with confirmation (press y/n) |
| `:model_set <id>` | Switch to a saved model configuration |
| `:model_load` | Load model configurations from default config file |

## Window Container

The terminal organizes concurrent streams into separate windows with synchronized widths:

- **Window Cursor**: Use `j`/`k` to navigate between windows. The cursor defaults to the newest window.
- **Auto-follow**: When new windows appear, cursor moves to them automatically. Pressing `k`, `g`, `H`, `L`, or `M` disables follow; returning to the last window re-enables it.
- **Fold mode**: Press `Space` to toggle fold mode on the active window, collapsing content to first line + indicator + last 3 lines.

## Task Queue Manager

When tasks (prompts or commands) are submitted while a previous task is still running, they are added to a queue. Press `Ctrl+Q` to open the task queue manager:

| Key | Action |
|-----|--------|
| `q`, `esc` | Close queue manager |
| `j`, `↓` | Move selection down |
| `k`, `↑` | Move selection up |
| `d` | Delete selected task |

Each queued task displays:
- Queue ID (Q1, Q2, etc.)
- Type: `P` (prompt) or `C` (command)
- Truncated content preview

## Session Persistence

- **Manual-save**: Sessions are saved only when you use `:save [filename]` or press `Ctrl+S`
- **Auto-save**: Enabled by default. When `--session` is specified, the session is automatically saved after each task completes. Use `--auto-save=false` to disable.
- **Load**: On startup, AlayaCore creates a new empty session unless you specify `--session` to load an existing one
- **Auto-summarize**: When `--auto-summarize` is enabled and `context_limit` is set, AlayaCore automatically triggers `:summarize` when context reaches 80% of the limit. Use `:summarize` to manually reduce context at any time.

Session files use TLV-encoded binary format with YAML frontmatter for metadata. See [architecture.md](architecture.md) for format details.

## Plain IO Mode

Use `--plainio` to run AlayaCore as a plain stdin/stdout process with no terminal UI. This is useful for scripting, piping, or headless environments.

### Input

- Each line from stdin is a separate prompt
- A trailing backslash (`\`) continues the prompt on the next line:

```
This is a single \
prompt that spans two lines.
```

- **Ctrl-D** (EOF): closes stdin, waits for queued tasks to finish, exits with code `0`
- **Ctrl-C** (SIGINT): sends `:cancel_all`, exits with code `1`
- Errors cause exit with a negative return code

### Output

All output is plain text with no ANSI codes:

| Content | Format |
|---------|--------|
| Assistant text | Printed directly |
| Reasoning | Printed directly |
| User prompts | `> prompt` |
| Tool calls | `[tool_name: args]` |
| Tool results | Suppressed |
| Errors | `Error: message` |
| Notifications | `[message]` |

A blank line separates messages of different types.

### Piped Example

```sh
echo "what is 2+2?" | alayacore --plainio
```
