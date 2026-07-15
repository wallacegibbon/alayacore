# Commands

AlayaCore provides colon-prefixed commands (`:command`) that work across all adapters — TUI, Plain IO, and Raw IO.

Commands fall into three categories:

- **Immediate commands** (`CmdImmediate`) — run synchronously in the main loop, always allowed
- **Idle commands** (`CmdIdle`) — run synchronously, but rejected while a task is in progress
- **Task commands** — require LLM calls, run in a separate goroutine

## Immediate Commands

| Command | Action |
|---------|--------|
| `:cancel` | Cancel current task |
| `:save [filename]` | Save session. Uses `--session` path if no filename given. |
| `:reason [0\|1\|2]` | Set reasoning level (0=off, 1=normal, 2=max). Default: 1 |
| `:theme_set <name>` | Switch to a different theme |
| `:confirm <id> yes\|no` | Confirm or deny a pending tool execution |
| `:fork <id> <filename>` | Fork session — save all content up to a history ID to a file |

## Idle Commands

These commands are rejected with an error if a task is currently running:

| Command | Action |
|---------|--------|
| `:model_set <id>` | Switch to a model by numeric ID |
| `:model_load` | Reload model configs from the config file |
| `:model_sync` | Apply edited model config (sent by UI, not user-facing) |
| `:video_config <fps> <0\|1>` | Set video FPS and resolution (0=default, 1=max) |

## Task Commands

These commands require LLM calls and run in a separate goroutine:

| Command | Action |
|---------|--------|
| `:continue` | Retry the last prompt |
| `:summarize` | Summarize conversation to reduce token usage ⚠️ **Replaces entire conversation history with a summary** — see [context-tracking.md](context-tracking.md) |

## Adapter-Specific Commands

Some commands are handled directly by each adapter and never reach the session command dispatch:

| Command | TUI | Plain IO | Raw IO |
|---------|-----|----------|--------|
| `:quit` / `:q` | Shows confirmation dialog | Exits immediately | Passed to session (adapter doesn't interpret frame payloads) |
| `:help` | Opens help window | Exits immediately (no TUI) | Passed to session |
| `:suspend` | Suspends process (Ctrl+Z) | Not supported | Passed to session |

## :fork Details

The `:fork` command saves all session content from the beginning up to (and including) a specific history ID to a new file. This is useful for extracting a conversation segment into a standalone session file.

```
:fork 42 ./extract.alaya
```

In the TUI, you can also press `Ctrl+F` at a window to pre-fill the `:fork` command with that window's history ID.

## :continue


See [error-handling.md](error-handling.md) for details on error recovery with `:continue`.

## :summarize

The `:summarize` command asks the LLM to produce a concise summary of the conversation, then replaces the entire message history with that summary. This is the only way to reduce context usage manually when auto-summarize is disabled.

> ⚠️ **Destructive** — The conversation history is replaced by the summary. Previous turns are lost. Consider saving first with `:save`.

See [context-tracking.md](context-tracking.md) for details.
