# Architecture

AlayaCore follows a layered architecture with clear separation of concerns. The layers communicate via a lightweight TLV (Tag-Length-Value) binary protocol.

## Components

### Entry Point (`main.go`, `internal/config/`, `internal/app/`)

The entry point wires together all components:

1. **`config.Parse()`** — Parses CLI flags into `config.Settings`
2. **`app.Setup()`** — Initializes shared components:
   - Skills manager (loads skill metadata from `--skill` directories)
   - Tools (`read_file`, `edit_file`, `write_file`, `execute_command`, and conditionally `search_content`)
   - System prompt (default + skills section/fragment when configured + current working directory)
3. **Adapter creation** — Starts the terminal, PlainIO, or RawIO adapter

### Session Layer (`internal/agent/`)

The session layer manages conversation state, task execution, and model interaction.

| Component | Description |
|-----------|-------------|
| `Session` | Main struct managing conversation state and message history |
| `Task Queue` | FIFO queue for pending prompts and commands |
| `ModelManager` | Loads and manages AI model configurations from `model.conf`. Never writes to the file. |
| `RuntimeManager` | Persists runtime settings (active model, active theme) to `runtime.conf` |
| `CommandDefinitions` | Static metadata for session commands (`:save`, `:cancel`, etc.) |
| `ContextTokens` | Tracks conversation context size across API calls. See [context-tracking.md](context-tracking.md). |

#### Concurrency Model

The session uses three goroutines for concurrent operation:

| Goroutine | Source | Role |
|-----------|--------|------|
| **Main loop** (`run()`) | `Session.Start()` | Owns all mutable state, manages the task queue, processes commands |
| **Input pump** (`inputPump()`) | launched by `run()` | Reads TLV frames from the input stream, sends parsed messages to the main loop |
| **Task worker** (`runTask()`) | spawned by `run()` per task | Executes a single task (LLM streaming + tool calls), returns final messages via `taskResult` |

**Cross-goroutine communication:**

| Mechanism | From | To | Purpose |
|-----------|------|----|---------|
| `msgCh` (buffered, cap 100) | inputPump | run() | Parsed user input messages |
| `taskCancel` (atomic.Value) | inputPump | task worker | Cancel the running task |
| `taskResult` (buffered, cap 1) | task worker | run() | Return final messages and signal task completion |
| `stateCh` (buffered, cap 64) | task worker | run() | Step progress, token counts, paused state |
| `infoUpdateCh` (buffered, cap 1) | task worker | run() | Request system-info broadcast |
| `atomic.Pointer[Agent]` | both | — | Lock-free agent pointer for task goroutine |
| `atomic.Pointer[llm.Provider]` | both | — | Lock-free provider pointer for task goroutine |
| `atomic.Int64` | both | — | ContextTokens, currentStep, reasoningLevel |
| `atomic.Bool` | both | — | pausedOnError (written by task goroutine) |

**Lifecycle — drain on EOF:**

When the input stream reaches EOF (e.g. a piped `echo` command closes stdin),
the inputPump closes `msgCh` and exits. If a task is still running, `run()`
enters `drainUntilTaskDone()` to process state events and the task completion
signal. After the current task finishes, any remaining queued tasks are
processed one by one before the session exits. This ensures that all output
(prompt echo, assistant response, tool results) is flushed and no queued
prompts are abandoned.

```
stdin EOF ──▶ inputPump closes msgCh ──▶ run() detects closed channel
                                                │
                                    ┌───────────┴───────────┐
                                    │  drain running task   │
                                    │  + queued tasks       │
                                    │  (loop until empty)   │
                                    └───────────┬───────────┘
                                                │
                                             return
```

**State ownership:**
- The `run()` goroutine is the sole owner of session state. It reads/writes `taskQueue`, `inProgress`, `pausedOnError`, `reasoningLevel`, `reasoningDirty`, and all command-handling logic. ModelManager and RuntimeManager are also accessed only from `run()`.
- The task goroutine communicates state changes via typed events on `stateCh` (step progress, token counts) and reads atomic fields (`agent`, `provider`, `ContextTokens`, `currentStep`, `reasoningLevel`, `reasoningDirty`, `pausedOnError`) for lock-free access. The final message state is returned via `taskResult` on completion.
- The input pump goroutine has NO access to session state except `taskCancel` (the per-task cancel func, for `:cancel` commands).
- `sendSystemInfo` runs only in the `run()` goroutine; the task goroutine requests updates via `infoUpdateCh`.

**Design rationale:** Tasks must run in a separate goroutine because LLM streaming is blocking (3-10s per step). If tasks ran synchronously in `run()`, the main loop could not process user input (`:cancel`, new prompts, immediate commands) during task execution. The per-task goroutine pattern keeps the main loop responsive while avoiding a persistent worker goroutine that would sit idle between tasks.

**Gotcha — atomic flags must use Load/Store:** `pausedOnError` is an `atomic.Bool`. Always use `.Load()` to read and `.Store()` to write. Direct assignment will not compile.

### Session Persistence

- **Auto-save** — Always enabled when `--session` is specified. The session is saved after each step completes. Redundant writes are skipped when message count and content are unchanged.
- **Manual save** — `:save [file]` or `Ctrl+S` at any time (TUI mode).
- **Load** — On startup, AlayaCore starts a new empty session unless you specify `--session` to load an existing one.
- **Auto-summarize** — When `--auto-summarize` is enabled and `context_limit` is set, AlayaCore automatically triggers `:summarize` when context reaches 65% of the limit.

Session files use a Markdown-based format with YAML frontmatter. The body contains TLV-encoded conversation data (messages, tool calls, tool results) written directly as binary TLV records after the frontmatter.

The frontmatter includes a `message_version` field that tracks the TLV message encoding format. When loading a session, it must match `MessageVersion` exactly — any mismatch is rejected. The version is also broadcast to adapters as the first `TagSystemMsg` frame on startup (`{"type":"version","data":{"message_version":3}}`), so they can validate format compatibility before processing subsequent messages.

**Message grouping on load:** The session format stores a flat sequence of TLV chunks with no explicit message boundaries. On load, chunks are grouped into messages by role: consecutive chunks with the same role are merged into a single message's `Content` array. This correctly handles multi-part user messages (e.g., when a user adds context after a failed prompt) and assistant messages containing reasoning + text + tool calls.

### Agent Layer (`internal/llm/`)

The agent layer handles LLM interaction and tool-calling orchestration.

| Component | Description |
|-----------|-------------|
| `Agent` | Tool-calling loop orchestration with configurable max steps |
| `Provider` interface | Streaming LLM abstraction with callback-based event handling |
| `Factory` | Creates the correct provider based on `protocol_type` |
| `Providers` | Anthropic and OpenAI implementations |
| `TypedExecute` | Type-safe tool execution via Go generics |
| `GenerateSchema` | Auto-generates JSON schemas from struct tags |

**Key pattern — Callback Streaming:**

```go
Agent.Stream(ctx, messages, llm.StreamCallbacks{
	OnTextDelta:      func(delta string, index int) error { ... },
	OnReasoningDelta: func(delta string, index int) error { ... },
	OnToolUseStart:  func(id, name string) error { ... },
	OnToolUseInput:       func(id string, input json.RawMessage) error { ... },
	OnToolUseOutput:     func(id string, output ToolResultOutput) error { ... },
	OnStepStart:      func(step int) error { ... },
	OnStepFinish:     func(msgs []Message, usage Usage) error { ... },
})
```

Messages are appended incrementally in `OnStepFinish` so they're preserved even if the user cancels.

### Tools Layer (`internal/tools/`)

| Tool | Description | Safety | Dependency |
|------|-------------|--------|------------|
| `read_file` | Read file contents with optional line ranges. 64KB max for full reads (truncates at line boundary with metadata). | Safe | — |
| `edit_file` | Search/replace edits on existing files | Medium | — |
| `write_file` | Create or overwrite files | Dangerous | — |
| `execute_command` | Execute commands in the detected shell (cross-platform). Large output (>64KB) saved to `.alayacore.tmp/cmd-*.txt`; only file path and metadata returned. | Most Dangerous | — |
| `search_content` | Search file contents using ripgrep (`rg`). Results exceeding `max_lines` (default 100) saved to `.alayacore.tmp/search-*.txt`; only match count and file path returned. | Safe | Requires `rg` binary |

Each tool is implemented with type-safe input structs and auto-generated JSON schemas. All tools accept a `context.Context` parameter and respect cancellation — `:cancel` will interrupt long-running tool execution. See [schema-improvements.md](schema-improvements.md) for the pattern.

The `search_content` tool is conditionally registered — it is only available when the `rg` binary is found on the system `PATH` at startup. When available, the system prompt includes an instruction to prefer `search_content` over reading files chunk by chunk to locate code and patterns. This instruction is omitted when `rg` is not installed.

#### Shell Detection (`internal/tools/shell/`)

The `execute_command` tool uses a cross-platform shell detection system. On startup, it probes the OS environment for an available shell and selects the best candidate.

**Detection order:**

1. `ALAYACORE_SHELL` environment variable (matched against known shells; unknown values are ignored)
2. OS-specific `knownShells` list tried in preference order (guaranteed to succeed — `sh` on Unix, `cmd` on Windows)

**Supported shells:**

| Shell | Binary | OS | Invocation | Notes |
|-------|--------|----|------------|-------|
| Bash | `bash` | Unix | `bash -c <cmd>` | Preferred on Unix; LLMs naturally write bash syntax |
| Zsh | `zsh` | Unix | `zsh -c <cmd>` | Second choice on Unix |
| POSIX sh | `sh` | Unix | `sh -c <cmd>` | Guaranteed on all POSIX systems |
| PowerShell Core | `pwsh` | Windows | `pwsh -NoLogo -NonInteractive -Command <cmd>` | Preferred on Windows |
| Windows PowerShell | `powershell` | Windows | `powershell -NoLogo -NonInteractive -Command <cmd>` | Ships with Windows |
| cmd | `cmd` | Windows | `cmd /c <cmd>` | Guaranteed on all Windows machines |

The tool description (shown to the LLM) is dynamically generated based on the detected shell so the LLM uses the correct syntax. Platform-specific process isolation is handled per-OS:

- **Unix**: `setsid` creates a new session; `SIGINT` → `SIGKILL` for cancellation
- **Windows**: `CREATE_NO_WINDOW` isolates the child; `process.Kill()` for cancellation

The package uses Go build tags (`//go:build !windows` / `//go:build windows`) for all OS-specific code.

## Cross-Platform Architecture

AlayaCore uses Go build tags for all OS-specific code. The only platform-dependent subsystem is shell execution, isolated in the `internal/tools/shell/` package:

| File | Build tag | Provides |
|------|-----------|----------|
| `shell.go` | *(all)* | `Shell` type, `Detect()`, `detect()` |
| `shell_unix.go` | `!windows` | Unix shell defs (`bash`, `zsh`, `sh`), `knownShells` |
| `shell_windows.go` | `windows` | Windows shell defs (`pwsh`, `powershell`, `cmd`), `knownShells` |
| `exec_unix.go` | `!windows` | `SetDetachFlags` (setsid), `OpenDevNull` (/dev/null) |
| `exec_windows.go` | `windows` | `SetDetachFlags` (CREATE_NO_WINDOW + CREATE_NEW_PROCESS_GROUP), `OpenDevNull` (NUL) |
| `terminate_unix.go` | `!windows` | `Job` (no-op), `AssignJob` (no-op), `ClearJob` (no-op), `SignalProcessGroup` (SIGINT; SIGKILL follow-up via `exec.Cmd.WaitDelay`) |
| `terminate_windows.go` | `windows` | `Job` type, `AssignJob`, `ClearJob`, `TerminateProcessGroup` (Job Object → taskkill /F /T → Kill) |

All other packages (LLM providers, session management, TLV protocol, skills, schema generation) are pure Go with no OS-specific code.

## System Prompt Architecture

The system prompt is sent as **separate messages** (not a single concatenated string):

```
System Message 1: Default Prompt (identity + rules + search preferences)
                  + Skills section (only when skills configured)
                  + Current working directory

System Message 2: Extra System Prompt (from --system flag, repeatable)
```

When `rg` is available, the default prompt includes an instruction to prefer the `search_content` tool for locating content over reading files chunk by chunk. This instruction is omitted when `rg` is not installed.

When skill paths are provided via `--skill` and skills are discovered, the prompt includes instructions for reading skill `SKILL.md` files from their `<location>`, followed by an `<available_skills>` XML fragment listing each skill's name, description, and location. Both are omitted entirely when no skills are configured.

Both providers (`openai`, `anthropic`) send these as two independent system
messages. The default prompt and extra prompt are kept separate so the LLM API
can cache them independently.

### Current Working Directory

The current working directory is appended to the end of System Message 1. This
placement has two benefits:

1. **Absolute path anchoring** — LLMs use the CWD to construct correct absolute
   paths from the first tool call onward, rather than guessing or assembling
   paths incorrectly. Empirical testing shows that without the CWD, LLMs still
   use absolute paths but occasionally construct them with the wrong base
   directory, wasting steps.

2. **Cache reuse** — The stable portion of the system prompt (identity, rules,
   skills) remains identical across sessions and can be served from the API
   cache. Only the CWD suffix changes between projects, minimizing cache misses.

The CWD is **not** persisted in the session file. On session load, it is
rebuilt from the current runtime environment, ensuring the LLM always sees the
correct base directory for the current session.

## Design Decisions

1. **TLV Protocol** — Simple binary protocol for clean separation between adapters and session. The TUI, plain-IO, and raw-IO modes all share the same session/agent logic.
2. **Task Queue** — Deferred task processing with cancellation support. Queued tasks execute sequentially.
3. **Virtual Scrolling** — Only visible windows are rendered. See [virtual-rendering-performance.md](virtual-rendering-performance.md).
4. **Typed Tools** — `TypedExecute[T]` wrapper for type-safe tool implementations with auto-generated schemas. See [schema-improvements.md](schema-improvements.md).
5. **Lazy Agent Init** — Agent and provider are created on first use, not at startup.
6. **Sequential Tool Execution** — Tools execute one at a time, avoiding race conditions. See [sequential-tool-execution.md](sequential-tool-execution.md).
7. **Context Efficiency** — Large outputs (>64KB) saved to `.alayacore.tmp/` instead of inline. See [truncation.md](truncation.md).
8. **Reasoning Mode** — Provider-specific thinking fields added to API requests. Three levels: 0=off, 1=normal, 2=max. Toggled via `:reason [0|1|2]`.
9. **Concurrent Task Execution** — Each task runs in its own goroutine so the main loop stays responsive during LLM streaming. Communication via typed channels and atomic fields.
10. **Filter-What-You-See** — Searchable list components (ModelSelector, HelpWindow) build a pre-computed, lowercased `searchStr` that concatenates all visible fields of each item. Filtering is a single `FuzzyMatch(term, searchStr)` against this string, ensuring the search always matches exactly what the user can see, including cross-field queries (e.g. typing "quitexit" matches `:quit` + `Exit application`).

