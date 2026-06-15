# Commands

AlayaCore provides colon-prefixed commands (`:command`) that work across all adapters — TUI, Plain IO, and Raw IO.

Commands fall into three scheduling categories:

- **Immediate commands** — run synchronously in the main loop, always allowed
- **When-idle commands** — run synchronously, but rejected while a task is in progress
- **Deferred commands** — enqueued at the front of the task queue; run when no task is active and can be canceled with `:cancel`

## Immediate Commands

| Command | Action |
|---------|--------|
| `:cancel` | Cancel current task |
| `:cancel_all` | Cancel current task and clear the task queue |
| `:save [filename]` | Save session. Uses `--session` path if no filename given. |
| `:reason [0\|1\|2]` | Set reasoning level (0=off, 1=normal, 2=max). Default: 1 |
| `:theme_set <name>` | Switch to a different theme |
| `:confirm <id> yes\|no` | Confirm or deny a pending tool execution |
| `:fork <id> <filename>` | Fork session — save all content up to a history ID to a file |
| `:taskqueue_get_all` | List all queued tasks |
| `:taskqueue_del <queue_id>` | Delete a queued task by ID |
| `:taskqueue_edit <queue_id> <content>` | Edit a queued task's content by ID |
| `:clear_queue` | Clear all queued tasks without canceling the current task |

## When-Idle Commands

These commands are rejected with an error if a task is currently running:

| Command | Action |
|---------|--------|
| `:model_set <id>` | Switch to a model by numeric ID |
| `:model_load` | Reload model configs from the config file |

## Deferred Commands

Deferred commands run in a task goroutine and can be canceled with `:cancel` while executing:

| Command | Action |
|---------|--------|
| `:continue [skip]` | Retry last prompt, or skip it with `skip` and resume the queue |
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
:fork 42 ./extract.md
```

In the TUI, you can also press `Ctrl+F` at a window to pre-fill the `:fork` command with that window's history ID.

## :continue

When a task fails or is canceled, `:continue` retries the last prompt. If the task queue has pending items, `:continue skip` discards the failed prompt and advances to the next queued task.

See [error-handling.md](error-handling.md) for details on error recovery with `:continue`.

## :summarize

The `:summarize` command asks the LLM to produce a concise summary of the conversation, then replaces the entire message history with that summary. This is the only way to reduce context usage manually when auto-summarize is disabled.

> ⚠️ **Destructive** — The conversation history is replaced by the summary. Previous turns are lost. Consider saving first with `:save`.

See [context-tracking.md](context-tracking.md) for details.
