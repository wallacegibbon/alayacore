# Concurrency Simplification — Completed

The following simplifications have been applied to the session concurrency
model in `internal/agent/`. See commit history for details:

| Commit | Change |
|--------|--------|
| `0a31cec` | **Minimize mutex scope** — `nextQueueID` and `nextPromptID` are goroutine-local counters, updated outside the mutex |
| `76a30ed` | **Atomic bools** — `inProgress` and `pausedOnError` changed from `bool` (mutex-guarded) to `atomic.Bool` |
| `8520f8f` | **Remove drainMessages** — The helper micro-optimization was unnecessary; the main loop processes pending messages naturally in its next select iteration |
| `b4f2ce2` | **Centralize system info** — `sendSystemInfo()` now runs only in the `run()` goroutine; the task goroutine requests updates via a dedicated `infoUpdateCh` channel |

## Current Architecture

```
Goroutines:
  run()           — main event loop, owns all state
  inputPump()    — reads TLV frames, sends parsed messages to run()
  runTask()      — spawned per task, executes LLM streaming + tool calls

Synchronization:
  sync.Mutex     — protects shared state (Messages, taskQueue, etc.)
  atomic.Bool    — lock-free access for inProgress, pausedOnError
  Channels       — msgCh, taskCancelCh, taskDone, infoUpdateCh, runDone
```

## Future Considerations

- **Full actor model** — The current design falls short of a pure actor model
  (one goroutine owns all state, everyone sends messages via channels). To get
  there, the task goroutine would need to send state mutations as messages
  instead of mutating shared state under the mutex. This eliminates the mutex
  entirely but requires restructuring the streaming callbacks in
  `processPrompt()`.
- **`:save` as immediate command** — Currently `:save` is a deferred command
  (queued through the task queue). Making it immediate would bypass the queue
  and allow saving even while a task is running.
- **Metrics** — Consider adding metrics for message processing latency.
