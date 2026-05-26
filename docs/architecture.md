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
3. **Adaptor creation** — Starts either the terminal or PlainIO adaptor

### Adaptors Layer (`internal/adaptors/`)

The adaptor layer handles user interaction and translates between user actions and the TLV protocol.

#### Terminal Adaptor (`internal/adaptors/terminal/`)

| Component | Description |
|-----------|-------------|
| `Terminal` | Main Bubble Tea model composing all UI components |
| `DisplayModel` | Renders assistant output with virtual scrolling. See [virtual-rendering-performance.md](virtual-rendering-performance.md). |
| `InputModel` | Handles user text input. See [external-editor-windowsize.md](external-editor-windowsize.md). |
| `Editor` | External editor operations (`$EDITOR`) for multi-line input, display viewing, and queue editing |
| `ModelSelector` | Modal for switching between AI models |
| `QueueManager` | Modal for managing the task queue |
| `ThemeSelector` | Modal for switching between color themes |
| `OutputWriter` | Parses TLV from session and renders styled content |
| `WindowBuffer` | Virtual scrolling buffer for display windows |
| `Theme` | Customizable color scheme (Catppuccin Mocha default) |

#### PlainIO Adaptor (`internal/adaptors/plainio/`)

Plain stdin/stdout mode, activated with `--plainio`. Shows assistant text, reasoning, and tool call headers. Suppresses tool result content. Reads prompts from stdin (one per line, backslash continuation for multi-line prompts).

#### File Naming Convention

Files in the adaptor packages are named from the **session's perspective**:

- **`input.go`** — builds the **input to the session**. Reads user data (keystrokes, stdin lines) and feeds it into the session's input channel via TLV-encoded messages.
- **`output.go`** — handles the **output from the session**. Receives TLV messages from the session and renders them to the user (TUI windows, stdout).

```
User IO ──▶ input.go ──▶ input channel ──▶ Session ──▶ output.go ──▶ User IO
             ("input to                       ("output from
              the session")                    the session")
```

Both adaptors follow this convention. Each adaptor provides its own implementation of how user IO maps to and from the session's TLV channels.

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
| `taskCancelCh` (buffered, cap 1) | inputPump | task worker | Cancel the running task |
| `taskResult` (buffered, cap 1) | task worker | run() | Return final messages and signal task completion |
| `stateCh` (buffered, cap 64) | task worker | run() | Step progress, token counts, paused state |
| `infoUpdateCh` (buffered, cap 1) | task worker | run() | Request system-info broadcast |
| `atomic.Pointer[Agent]` | both | — | Lock-free agent pointer for task goroutine |
| `atomic.Pointer[llm.Provider]` | both | — | Lock-free provider pointer for task goroutine |
| `atomic.Int64` | both | — | ContextTokens, currentStep, reasoningLevel |
| `atomic.Bool` | both | — | pausedOnError (written by task goroutine) |

**Lifecycle — drain on EOF:**

When the input stream reaches EOF (e.g. a piped `echo` command closes stdin),
the inputPump closes `msgCh` and exits. If a task is still in progress, `run()`
does not return immediately — it enters `drainUntilTaskDone()`, which processes
state events and the task completion signal before letting the session exit.
This ensures that all output (prompt echo, assistant response, tool results) is
flushed before the process terminates.

```
stdin EOF ──▶ inputPump closes msgCh ──▶ run() detects closed channel
                                                │
                                           [task running?]
                                           /              \
                                         yes              no
                                          │                │
                              drainUntilTaskDone()      return
                                   │
                            wait for taskResult
                            + process state events
                                   │
                                return

**State ownership:**
- The `run()` goroutine is the sole owner of session state. It reads/writes `taskQueue`, `inProgress`, `pausedOnError`, `reasoningLevel`, `reasoningDirty`, and all command-handling logic. ModelManager and RuntimeManager are also accessed only from `run()`.
- The task goroutine communicates state changes via typed events on `stateCh` (step progress, token counts) and reads atomic fields (`agent`, `provider`, `ContextTokens`, `currentStep`, `reasoningLevel`, `reasoningDirty`, `pausedOnError`) for lock-free access. The final message state is returned via `taskResult` on completion.
- The input pump goroutine has NO access to session state except `taskCancelCh` (for `:cancel` commands).
- `sendSystemInfo` runs only in the `run()` goroutine; the task goroutine requests updates via `infoUpdateCh`.

**Design rationale:** Tasks must run in a separate goroutine because LLM streaming is blocking (3-10s per step). If tasks ran synchronously in `run()`, the main loop could not process user input (`:cancel`, new prompts, immediate commands) during task execution. The per-task goroutine pattern keeps the main loop responsive while avoiding a persistent worker goroutine that would sit idle between tasks.

### Session Persistence

- **Auto-save** — Always enabled when `--session` is specified. The session is saved after each step completes. Redundant writes are skipped when message count and content are unchanged.
- **Manual save** — `:save [file]` or `Ctrl+S` at any time (TUI mode).
- **Load** — On startup, AlayaCore starts a new empty session unless you specify `--session` to load an existing one.
- **Auto-summarize** — When `--auto-summarize` is enabled and `context_limit` is set, AlayaCore automatically triggers `:summarize` when context reaches 65% of the limit.

Session files use a Markdown-based format with YAML frontmatter. The body contains TLV-encoded conversation data (messages, tool calls, tool results) written directly as binary TLV records after the frontmatter.

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
	OnTextDelta:      func(delta string) error { ... },
	OnReasoningDelta: func(delta string) error { ... },
	OnToolCallStart:  func(id, name string) error { ... },
	OnToolCall:       func(id, name string, input json.RawMessage) error { ... },
	OnToolResult:     func(id string, output ToolResultOutput) error { ... },
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

#### Exit Codes on Cancellation and Timeout

When a command is canceled (via `:cancel`) or times out (2-minute default), AlayaCore terminates the process tree and reports the exit code to the user:

- **Unix**: `SIGINT` is sent first (exit code `130` = 128+2). If the process doesn't exit within 2 seconds, `SIGKILL` is sent (exit code `137` = 128+9). These follow the Unix convention of `128 + signal_number`.
- **Windows**: `TerminateJobObject` is called with exit code `1`. If the Job Object is unavailable (e.g. nested job restriction on pre-Win8), the fallback `taskkill /F /T` also defaults to exit code `1`. This is the conventional "abnormal termination" value on Windows, where there is no Unix-style signal concept. In all paths, `ExitCodeFromError` extracts this from `exec.ExitError.ExitCode()`.

When a command completes normally but with a non-zero exit code, AlayaCore reports the actual exit code returned by the process (via `ExitCodeFromError`).

The package uses Go build tags (`//go:build !windows` / `//go:build windows`) for all OS-specific code.

#### Resource Safety

The `execute_command` tool manages OS resources with the following guarantees on every code path — normal completion, non-zero exit, cancellation (`:cancel`), and timeout:

| Resource | Acquired via | Released via |
|---|---|---|
| Child process | `cmd.Start()` | `cmd.Wait()` — always called; the buffered `done` channel and `TerminateProcessGroup` ensure every path drains it |
| Stdin handle (`/dev/null` or `NUL`) | `OpenDevNull()` | `defer devNull.Close()` |
| Windows Job Object | `AssignJob()` | `defer job.Close()` + `KILL_ON_JOB_CLOSE` (kernel-level guarantee even on crash) |
| `cmd.Wait()` goroutine | `go func()` | Buffered channel (`make(chan error, 1)`) ensures the send never blocks; process kill ensures `cmd.Wait()` returns |
| Timeout context | `context.WithTimeout()` | `defer timeoutCancel()` |
| Temp file handle (>64KB output) | `os.CreateTemp()` | `defer file.Close()` |

**Key invariants (do not break when modifying):**
- `cmd.Wait()` MUST be called on every path — the `done` channel must remain buffered.
- `TerminateProcessGroup` must always drain `done` after killing the process.
- The Job Object handle must be closed on every path (`defer` after nil-check covers this).

## TLV Protocol

Communication between adaptors and session uses a simple Tag-Length-Value (TLV) binary protocol.

### Message Format

```
[2-byte tag][4-byte length (big-endian)][value bytes]
```

### Delta Messages

TA, TR, and FS are **delta messages** — they arrive piece-by-piece during
streaming and carry a NUL-delimited stream ID in the value:

```
\x00<stream-id>\x00<content>
```

NUL bytes (`\x00`) are used as delimiters because they can never appear in
normal UTF-8 text, making the split unambiguous even if the LLM generates
content that looks like a stream ID.

Stream ID formats differ by tag:

- **TA, TR** — `<promptID>-<step>-<suffix>` where suffix is `t` (text) or `r` (reasoning).
  Example: `\x000-1-t\x00Hello world`

- **FS** — free-form tool call ID assigned by the LLM provider (e.g. `call_abc123`).
  Example: `\x00call_abc123\x00pending`

### Tags

| Tag | Code | Direction | Description |
|-----|------|-----------|-------------|
| `TagTextUser` | TU | Input | User text input |
| `TagTextAssistant` | TA | Output | Assistant text output |
| `TagTextReasoning` | TR | Output | Reasoning/thinking content |
| `TagFunctionCall` | FC | Output | Function call for persistence |
| `TagFunctionResult` | FR | Output | Function result for persistence |
| `TagFunctionState` | FS | Output | Function state indicator (pending/success/error) |
| `TagSystemError` | SE | Output | System error messages |
| `TagSystemNotify` | SN | Output | System notifications |
| `TagSystemData` | SD | Output | System data (JSON) |

### Example Flow

```
1. User types "read main.go" in terminal
2. Terminal adaptor emits: TLV(TU, "read main.go")
3. Session reads TLV, creates UserPrompt task
4. Session processes prompt through the agent loop
5. Agent calls read_file tool → Session emits: TLV(FS, "\x00tool123\x00pending") → TLV(FS, "\x00tool123\x00success")
6. Agent generates response → Session emits: TLV(TA, "\x000-0-t\x00Here's what main.go does...")
7. Terminal adaptor parses TLV, renders styled content in windows
```

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

## Data Flow

### Startup Flow

```
main.go → config.Parse() → Settings
                ↓
        app.Setup(Settings)
                ↓
        ├── skills.NewManager(skillPaths)
        ├── tools.NewReadFileTool(), etc.
        ├── tools.RGAvailable() → conditionally register search_content tool
        └── Build system prompt (with SEARCH section if rg available)
                ↓
        terminal.NewAdaptor(appConfig)  or  plainio.NewAdaptor(appConfig)
                ↓
        Session created with tools and system prompt
```

### User Prompt Flow

```
User types prompt
  → InputModel captures input
    → Emit TLV(TU, prompt)
      → inputPump reads TLV
        → submitTask(UserPrompt)
          → Task Queue
            → runTask() (task goroutine)
              → handleUserPrompt()
                → processPrompt()
                  → Agent.Stream()
                    → Callbacks emit TLV(TA), TLV(TR), TLV(FS), etc.
                      → OutputWriter parses TLV
                        → WindowBuffer.AppendOrUpdate()
                          → DisplayModel.View()
                            → Terminal renders output
```

### Tool Execution Flow

```
Agent.Stream() receives tool_call event
  → OnToolCallStart callback → TLV(FC, placeholder) → UI shows tool window immediately
    → OnToolCall callback → TLV(FC, full input) → UI replaces placeholder content
      → Agent executes tool: tool.Execute(ctx, input)
        → OnToolResult callback → TLV(FS, "\x00<id>\x00success") → UI updates indicator
          → Tool result added to messages
            → Agent continues to next step (if under max_steps)
```

## Design Decisions

1. **TLV Protocol** — Simple binary protocol for clean separation between adaptors and session. Both the TUI and plain-IO mode share all session/agent logic.
2. **Task Queue** — Deferred task processing with cancellation support. Queued tasks execute sequentially.
3. **Virtual Scrolling** — Only visible windows are rendered. 3.5x faster than naive rendering. See [virtual-rendering-performance.md](virtual-rendering-performance.md).
4. **Domain Errors** — Structured error types with operation context for consistent error handling. See [error-handling.md](error-handling.md).
5. **Command Definitions** — Static metadata table for colon-commands, dispatch via `switch`.
6. **Interface Abstraction** — OutputWriter interface for testability.
7. **Provider Factory** — Decoupled provider creation from session logic.
8. **Typed Tools** — `TypedExecute[T]` wrapper for type-safe tool implementations with auto-generated schemas.
9. **Lazy Agent Init** — Agent and provider are created on first use, not at startup.
10. **Sequential Tool Execution** — Tools execute one at a time. See [sequential-tool-execution.md](sequential-tool-execution.md).
11. **Context Efficiency** — Tool descriptions are minimal. Large outputs (>64KB) saved to `.alayacore.tmp/`: `read_file` truncates inline with metadata, `execute_command` and `search_content` save full output to file. See [truncation.md](truncation.md).
12. **Reasoning Mode** — Provider-specific reasoning fields are added to API requests. Three levels: 0=off, 1=normal, 2=max. Toggled via `:reason [0|1|2]`.
13. **Concurrent Task Execution** — Each task runs in its own goroutine so the main loop remains responsive to user input during LLM streaming. The task goroutine communicates state changes via typed channel events (`stateCh`). Lock-free atomic fields (`atomic.Pointer`, `atomic.Int64`, `atomic.Bool`) are used for the few values that the task goroutine reads directly (agent, provider, context tokens, reasoning level). No `sync.Mutex` is needed.
14. **Centralized System Info** — `sendSystemInfo()` runs only in the main loop goroutine. The task goroutine requests updates via a buffered channel (`infoUpdateCh`), which also wakes the main loop from its select to process the update promptly.
15. **Goroutine-Local Counters** — Fields accessed by a single goroutine (`nextQueueID` on the main loop, `nextPromptID` on the task goroutine) are plain fields with no synchronization needed.
16. **Drain on EOF** — When stdin is closed (EOF from a piped command), the session waits for the currently running task to finish before exiting. This ensures piped input produces visible output even though the pipe closes before the API responds. See `drainUntilTaskDone()` in `session_task.go`.

## Gotchas

Non-obvious patterns that have caused bugs. Read carefully when modifying related code.

### Agent step messages must not be reconstructed

The provider's `StepCompleteEvent.Messages` contains the complete assistant message — text, reasoning, and tool calls all together. The agent must use these messages as-is, not reconstruct a new message from just the tool calls. Reconstruction loses text content that the LLM returned alongside tool calls. See `agent.go` → `processStreamEvents`.

### Sentinel values must never be overwritten

`WindowBuffer.dirtyIndex` uses a sentinel (`dirtyFullRebuild = -2`) to signal that all windows need recalculation. State transitions must check whether the sentinel is already set before overwriting — an `else` branch that blindly assigns a new index can downgrade a full-rebuild to a single-window update, silently dropping windows from the display. See `window.go` → `markDirty`.

### Atomic flags must use Load/Store

`pausedOnError` is an `atomic.Bool`. Always use `.Load()` to read
and `.Store()` to write. Direct assignment will not compile.

### OpenAI tool call chunking

Tool arguments arrive in chunks across multiple delta events:
- First chunk: has `id` and `name`
- Subsequent chunks: `id: ""` but correct `index`
- **Must use `index` (not `id`) to associate chunks** — see `openAIStreamState.appendToolCallArgs()`
- When sending back in history, arguments must be JSON-string (not raw JSON) — see `openaiConvertToolCalls()`

### Null arguments in tool call chunks

Some providers emit no-op deltas with `"arguments": null` (JSON literal null):

```json
{
	"choices": [{
		"delta": {
			"tool_calls": [{
				"function": {"arguments": null},
				"id": "",
				"index": 0,
				"type": "function"
			}]
		},
		"index": 0
	}]
}
```

After `json.Unmarshal` into `json.RawMessage`, `args` becomes the 4 bytes `null`. Since `args[0]` is `'n'` (not `'"'`), it bypasses the unquote path and falls through to the raw append. Without a guard, the accumulated arguments become e.g. `{"path": "README.md"}null` — corrupting the JSON and causing tool execution to fail.

**Fix:** skip chunks where `string(args) == "null"`. Safe because the `arguments` field is always a JSON string type in the OpenAI API spec, so the only time `args[0] != '"'` is for the null literal. See `openAIStreamState.appendToolCallArgs()`.

### Terminal scroll position

`DisplayModel.autoFollow` must be set to `false` for K (line scroll up) and Ctrl+D/Ctrl+U (half-page scroll) via `MarkUserScrolled()`, not just k/g/H/M (cursor move via `MoveWindowCursorUp`/`SetWindowCursor`/`MoveWindowCursorToTop`/`MoveWindowCursorToCenter`), or auto-follow is not properly disabled on manual scrolling. When auto-follow is active, j (`MoveWindowCursorDown`) and L (`MoveWindowCursorToBottom`) are no-ops. J (`scrollDownLine`) and Ctrl+D (`scrollDownHalf`) are also no-ops when already at the bottom via an `AtBottom()` check — this prevents a race where a new window appended between ticks would let j/L silently jump to an invisible window and kill auto-follow, and avoids disabling auto-follow for no-op scrolls at the bottom. Only `G` (`SetCursorToLastWindow`) re-enables auto-follow. Other navigation (k, H, M, g, f, b) checks whether the cursor actually moves before disabling auto-follow — a no-op press at the boundary preserves auto-follow. See `display.go` → `MarkUserScrolled`.

### Incomplete tool calls on cancel

When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolCalls()` removes these to prevent API errors on next request.

### Tool result message ordering

`OnStepFinish` callback receives complete step messages. For tool-using steps, this includes both the assistant message (with tool calls) AND the tool result message. The `OnToolResult` callback should only send UI notifications, not append to session messages — the agent loop handles message assembly.

### ANSI escape sequences are not recursive

When styling text with lipgloss, each segment must be rendered individually before concatenation. You cannot render a string that already contains ANSI codes with a new style and expect it to work.

### Reasoning mode and reasoning_content

When reasoning mode is set via `:reason [0|1|2]`, each provider sends explicit thinking configuration in API requests. The key differences are:

1. A top-level **`thinking`** field (`{"type": "enabled"}` or `{"type": "disabled"}`) controls whether reasoning is active. This is always set explicitly — even when reasoning is off — because some providers (e.g. DeepSeek V4) default to thinking enabled. Omitting the field would leave thinking on at the API level, contradicting the UI state.
2. When reasoning mode is on (level 1 or 2), assistant messages that only contain tool calls must still include an **empty reasoning block** (required by DeepSeek and similar providers).

| Provider | Level 1 (normal) | Level 2 (max) | Disabled |
|----------|------------------|---------------|----------|
| **Anthropic** | `"thinking": {"type": "enabled"}`, `"output_config": {"effort": "high"}` | `"thinking": {"type": "enabled"}`, `"output_config": {"effort": "max"}` | `"thinking": {"type": "disabled"}` |
| **OpenAI-compatible** | `"thinking": {"type": "enabled"}`, `"reasoning_effort": "high"` | `"thinking": {"type": "enabled"}`, `"reasoning_effort": "xhigh"` | `"thinking": {"type": "disabled"}` |

> **Note:** The OpenAI-compatible thinking/reasoning parameters (`thinking`, `reasoning_effort`, `reasoning_content`) are not part of the official OpenAI API standard. They originate from [DeepSeek's thinking mode documentation](https://api-docs.deepseek.com/guides/thinking_mode) and are supported by **DeepSeek**, **GLM**, and **MiniMax**. Other providers silently ignore unknown fields.

#### OpenAI-compatible — request examples

When reasoning mode is **disabled**, assistant messages contain only the tool calls — no `reasoning_content` field:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"tool_calls": [{
				"function": {
					"arguments": "{\"path\":\"/home/wallace/playground/alayacore/go.mod\",\"end_line\":5}",
					"name": "read_file"
				},
				"id": "call_ca6eef24512147a6a9dae7bd",
				"index": 0,
				"type": "function"
			}]
		},

		...

	],

	"model": "deepseek-v4-flash",

	"thinking": { "type": "disabled" },

	...
}
```

When reasoning mode is **enabled**, every assistant message is padded with `"reasoning_content": ""` even when there is no actual reasoning text, and the request includes `reasoning_effort`:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"reasoning_content": "",
			"tool_calls": [{
				"function": {
					"arguments": "{\"path\":\"/home/wallace/playground/alayacore/go.mod\",\"end_line\":5}",
					"name": "read_file"
				},
				"id": "call_ca6eef24512147a6a9dae7bd",
				"index": 0,
				"type": "function"
			}]
		},

		...

	],

	"model": "deepseek-v4-flash",

	"thinking": { "type": "enabled" },
	"reasoning_effort": "xhigh",

	...
}
```

#### Anthropic-compatible — request examples

When reasoning mode is **disabled**, assistant messages contain only the tool-use content block — no `thinking` block:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"content": [
				{
					"id": "call_ca6eef24512147a6a9dae7bd",
					"input": {
						"end_line": 5,
						"path": "/home/wallace/playground/alayacore/go.mod"
					},
					"name": "read_file",
					"type": "tool_use"
				}
			]
		},

		...

	],

	"model": "deepseek-v4-pro",

	"thinking": { "type": "disabled" },

	...
}
```

When reasoning mode is **enabled**, every assistant message is prepended with an empty `{"type": "thinking", "thinking": ""}` block when none is present, and the request includes `output_config`:

```json
{
	"messages": [

		...

		{
			"role": "assistant",
			"content": [
				{
					"thinking": "",
					"type": "thinking"
				},
				{
					"id": "call_ca6eef24512147a6a9dae7bd",
					"input": {
						"end_line": 5,
						"path": "/home/wallace/playground/alayacore/go.mod"
					},
					"name": "read_file",
					"type": "tool_use"
				}
			]
		},

		...

	],

	"model": "deepseek-v4-pro",

	"thinking": { "type": "enabled" },
	"output_config": { "effort": "max" },

	...
}
```

Some OpenAI-compatible providers (e.g. DeepSeek) return `reasoning_content` in assistant responses. Per [DeepSeek's documentation](https://api-docs.deepseek.com/guides/thinking_mode):

> Between two user messages, if the model performed a tool call, the intermediate assistant's `reasoning_content` must participate in the context concatenation and must be passed back to the API in all subsequent user interaction turns.

This means **all** intermediate assistant messages in a multi-turn tool call chain must include their `reasoning_content`. Dropping it causes a 400 error from providers that require it.

#### Empty reasoning block padding — implementation

Both providers pad assistant messages with an empty reasoning value — but **only when reasoning mode is enabled** — to avoid wasting input tokens when it isn't needed.

- **Anthropic provider** (`anthropicConvertMessages`): prepends an empty `{"type": "thinking", "thinking": ""}` block to every assistant message that lacks one. The thinking block must come first per Anthropic's API.
- **OpenAI provider** (`openaiConvertMessages`): extracts reasoning text via `openaiExtractReasoning()` and sets `reasoning_content` on every assistant message — even as empty string when no reasoning text exists.

Both are conditional on reasoning mode being enabled. When reasoning mode is off, no padding is added.


