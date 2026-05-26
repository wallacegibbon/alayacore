# Adaptor Architecture

The adaptor layer handles user interaction and translates between user actions and the TLV protocol.

## Adaptors

### Terminal Adaptor (`internal/adaptors/terminal/`)

| Component | Description |
|-----------|-------------|
| `Terminal` | Main Bubble Tea model composing all UI components |
| `DisplayModel` | Renders assistant output with virtual scrolling |
| `InputModel` | Handles user text input |
| `Editor` | External editor operations (`$EDITOR`) for multi-line input, display viewing, and queue editing |
| `ModelSelector` | Modal for switching between AI models |
| `QueueManager` | Modal for managing the task queue |
| `ThemeSelector` | Modal for switching between color themes |
| `OutputWriter` | Parses TLV from session and renders styled content |
| `WindowBuffer` | Virtual scrolling buffer for display windows |
| `Theme` | Customizable color scheme (Catppuccin Mocha default) |

### PlainIO Adaptor (`internal/adaptors/plainio/`)

Plain stdin/stdout mode, activated with `--plainio`. Shows assistant text, reasoning, and tool call headers. Suppresses tool result content. Reads prompts from stdin (one per line, backslash continuation for multi-line prompts).

### File Naming Convention

Files in the adaptor packages are named from the **session's perspective**:

- **`input.go`** — builds the **input to the session**. Reads user data (keystrokes, stdin lines) and feeds it into the session's input channel via TLV-encoded messages.
- **`output.go`** — handles the **output from the session**. Receives TLV messages from the session and renders them to the user (TUI windows, stdout).

```
User IO ──▶ input.go ──▶ input channel ──▶ Session ──▶ output.go ──▶ User IO
```

Both adaptors follow this convention. Each provides its own implementation of how user IO maps to and from the session's TLV channels.

## Communication Pattern

All adaptors communicate with the session through the TLV stream protocol:

```
Adaptor → Session:  streamInput (ChanInput)  — user text, commands
Session → Adaptor:  Output (io.Writer)       — TLV-encoded events
```

This is the **only** runtime channel. The session never calls adaptor methods, and the adaptor never calls session methods during normal operation.

### Theme Persistence

The session persists the active theme via `RuntimeManager` and communicates it to the terminal adaptor through TLV as part of `SystemInfo.ActiveTheme`. The plainio adaptor ignores it since it has no visual rendering. On startup, the terminal reads the initial theme from the first `TagSystemData` message (defaulting to `"theme-dark"`).

## TLV Protocol

Communication between adaptors and session uses a simple Tag-Length-Value (TLV) binary protocol.

### Message Format

```
[2-byte tag][4-byte length (big-endian)][value bytes]
```

### Delta Messages

TA, TR, and FS are **delta messages** — they arrive piece-by-piece during streaming and carry a NUL-delimited stream ID in the value:

```
\x00<stream-id>\x00<content>
```

NUL bytes (`\x00`) are used as delimiters because they can never appear in normal UTF-8 text, making the split unambiguous even if the LLM generates content that looks like a stream ID.

Stream ID formats differ by tag:

- **TA, TR** — `<promptID>-<step>-<suffix>` where suffix is `t` (text) or `r` (reasoning). Example: `\x000-1-t\x00Hello world`
- **FS** — free-form tool call ID assigned by the LLM provider (e.g. `call_abc123`). Example: `\x00call_abc123\x00pending`

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

## Adaptor Bootstrap

The `StartSession()` function in `app/session.go` handles shared initialization for all adaptors:

- `session.InitError()` — fatal initialization check (--model flag validation)
- `session.ModelManager.GetLoadErrors()` — print config warnings
- `session.HasModels()` — abort if no models configured

This is setup code, not runtime coupling. Once the program starts, the adaptor only interacts with the session via TLV.
