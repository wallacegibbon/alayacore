# Adaptor Architecture

The adaptor layer handles user interaction and translates between user actions and the TLV protocol.

## Adaptors

### Terminal Adaptor (`internal/adapters/terminal/`)

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

### PlainIO Adaptor (`internal/adapters/plainio/`)

Plain stdin/stdout mode, activated with `--plainio`. Shows assistant text, reasoning, and tool call headers ("call" type only, "start" type frames silently ignored). Suppresses tool result content. Displays tool_confirm system messages as plain text prompts. Reads prompts from stdin (one per line, backslash continuation for multi-line prompts).

### RawIO Adaptor (`internal/adapters/rawio/`)

Raw TLV stdin/stdout mode, activated with `--rawio`. Reads and writes raw TLV-encoded frames directly, with no text parsing or formatting. Designed for parent programs that speak the TLV protocol to control AlayaCore programmatically.

### File Naming Convention

Files in the adapter packages are named from the **session's perspective**:

- **`input.go`** — builds the **input to the session**. Reads user data (keystrokes, stdin lines) and feeds it into the session's input channel via TLV-encoded messages.
- **`output.go`** — handles the **output from the session**. Receives TLV messages from the session and renders them to the user (TUI windows, stdout).

The rawio adapter is an exception — it's a single `adaptor.go` since both directions are trivial one-liners (`io.Copy` in, `os.Stdout.Write` out).

```
User IO ──▶ input.go ──▶ input channel ──▶ Session ──▶ output.go ──▶ User IO
```

## Communication Pattern

All adapters communicate with the session through the TLV stream protocol:

```
Adaptor → Session:  inputWriter (io.WriteCloser)  — user text, commands (TLV)
Session → Adaptor:  output (io.Writer)            — TLV-encoded events
```

This is the **only** runtime channel. The session never calls adaptor methods, and the adaptor never calls session methods during normal operation.

### Theme Persistence

The session persists the active theme via `RuntimeManager` and communicates it to the terminal adapter through TLV as a `TagSystemMsg` with type `"theme"`. The plainio and rawio adapters ignore it since they have no visual rendering. On startup, the terminal reads the initial theme from the first `"theme"` message (defaulting to `"theme-dark"`).

Theme changes flow through the session to keep a single source of truth:

1. `:theme_set <name>` (typed by user) or theme selector confirm both send the command to the session
2. Session persists the theme name via `RuntimeManager.SetActiveTheme()` and broadcasts the updated theme via a `TagSystemMsg` TLV message (`{"type":"theme","data":{"name":"..."}}`)
3. The terminal detects the theme change in `updateStatus()` and calls `applyTheme()` to load the theme file and update all UI component styles

This ensures both paths converge: the theme selector's live preview still applies themes directly for responsiveness, but the final commit always goes through the session.

## TLV Protocol

See [`tlv-samples/README.md`](../tlv-samples/README.md) for the full TLV
protocol spec — wire format, tags, delta messages, function lifecycle,
and binary samples for every message type.

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
        terminal.NewAdaptor(appConfig)  or  plainio.NewAdaptor(appConfig)  or  rawio.NewAdaptor(appConfig)
                ↓
        Session created with tools and system prompt
```

### User Prompt Flow

```
User types prompt
  → InputModel captures input
    → Emit TLV(UT, prompt)
      → inputPump reads TLV
        → submitTask(QueueItem{Type: "prompt", Content: text, Images: images})
          → Task Queue
            → runTask() (task goroutine)
              → handleUserPrompt()
                → processPrompt()
                  → Agent.Stream()
                    → Callbacks emit TLV(AT), TLV(AR), TLV(AF), TLV(UF), etc.
                      → OutputWriter parses TLV
                        → WindowBuffer.AppendOrUpdate()
                          → DisplayModel.View()
                            → Terminal renders output
```

### Tool Execution Flow

```
Agent.Stream() receives tool_call event
  → OnToolCallStart callback → TLV(AF, {"id":"<id>","type":"start","name":"<tool>"}) → UI shows tool name immediately
    → OnToolCall callback → TLV(AF, {"id":"<id>","type":"call","name":"<tool>","input":"..."}) → UI fills in arguments
      → Agent executes tool: tool.Execute(ctx, input)
        → OnToolResult callback → TLV(UF, {"id":"<id>","output":"...","status":"success"}) → UI shows output and indicator
          → Tool result added to messages
            → Agent continues to next step (if under max_steps)
```

## Adaptor Bootstrap

The `StartSession()` function in `app/session.go` handles shared initialization for all adapters:

- Creates the input pipe internally (`SliceBuffer`), returning the write end (`io.WriteCloser`) to the adaptor so it can feed TLV messages to the session
- `session.InitError()` — fatal initialization check (--model flag validation)
- `session.ModelManager.GetLoadErrors()` — print config warnings
- `session.HasModels()` — abort if no models configured

This is setup code, not runtime coupling. Once the program starts, the adaptor only interacts with the session via TLV.
