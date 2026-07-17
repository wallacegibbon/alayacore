# TUI Architecture: Elm, Bubble Tea, and AlayaCore's Design

## The Elm Architecture (Reference Model)

The Elm architecture is built on three core concepts:

```
Model  →  application state (single value)
Update →  pure function: (Model, Msg) → (Model, Cmd)
View   →  pure function: Model → Html (rendering)
```

Properties:
- **Immutable state** — `Update` returns a new `Model`, never mutates the old one
- **Side effects as data** — `Cmd` describes what to do, not how to do it (inspectable record)
- **Same-frame Cmd processing** — Runtime can inspect Cmd data and recursively call `update` within the same frame before rendering

## Bubble Tea: Key Differences from Elm

```go
type Cmd func() Msg  // not data — an opaque function
```

| Aspect | Elm | Bubble Tea | Consequence |
|--------|-----|------------|-------------|
| Cmd | Data (inspectable record) | `func() Msg` (opaque) | BT cannot inspect Cmd; renders before executing it |
| Msg dispatch | Sum types, exhaustive | `interface{}` + type switch | No compiler guarantee |
| Same-frame Cmd | Yes — runtime recurses before render | No — renders first, executes Cmd after | Continuous UI events must bypass Cmd to avoid 1-frame delay |

## Architecture Overview

```
Terminal (value type, root model)
├── Update(msg tea.Msg) → (tea.Model, tea.Cmd)     ← single entry point
│
├── Dispatches messages to components:
│   ├── KeyMsg  → handleKeyMsg → overlay.Update(msg)
│   ├── ThemeSelectedMsg  → emit theme_set command
│   ├── ModelSelectedMsg  → emit model_set command
│   ├── ConfirmResultMsg  → handleConfirmResult
│   ├── HelpCmdMsg        → focus input with command
│   ├── AttachmentSelectedMsg → addAttachment
│   ├── OverlayClosedMsg  → restoreFocus
│   ├── PasteMsg   → handlePaste (attachment window or input)
│   ├── BlurMsg    → handleBlur
│   ├── FocusMsg   → handleFocus
│   ├── WindowSize → handleWindowSize
│   └── default (unknown msg) → stderr log
│
├── Components (each has Update returning tea.Cmd):
│   ├── ConfirmDialog     Update(msg tea.Msg) → (ConfirmDialog, tea.Cmd)
│   ├── ThemeSelector     Update(msg tea.Msg) → (ThemeSelector, tea.Cmd)
│   ├── ModelSelector     Update(msg tea.Msg) → (ModelSelector, tea.Cmd)
│   ├── HelpWindow        Update(msg tea.Msg) → (HelpWindow, tea.Cmd)
│   ├── AttachmentWindow  Update(msg tea.Msg) → (AttachmentWindow, tea.Cmd)
│   ├── PromptInput       Update(msg tea.Msg) → (PromptInput, tea.Cmd)
│   └── InputField        Update(msg tea.Msg) → (InputField, tea.Cmd)
│
├── Code reuse units (pure functions, no tea.Cmd):
│   ├── FilteredListCore  HandleKey(msg tea.KeyMsg) → (Self, FilteredListResult)
│   └── ScrollableListCore HandleKey(msg tea.KeyMsg) → (Self, ScrollableListResult)
│
└── External systems (via interfaces/pointers):
    ├── out         OutputWriter    (session output, shared mutable)
    ├── streamInput io.WriteCloser  (TLV pipe to session)
    └── themeManager *ThemeManager  (theme file cache)

```

## Component vs Code Reuse Unit

### Components
- Have their own lifecycle (open/close)
- Communicate with Terminal via messages (ThemeSelectedMsg, etc.)
- All have `Update(msg tea.Msg) → (Self, tea.Cmd)`

### Code Reuse Units (FilteredListCore, ScrollableListCore)
- Cannot exist independently — embedded into components
- Have `HandleKey(msg tea.KeyMsg) → (Self, Result)` — no tea.Cmd
- Used for continuous UI operations (scrolling, filtering) where
  a 1-frame delay from Cmd routing would cause perceptible lag
- This is NOT a hack; Elm does the same thing with pure helper functions.
  The difference is that Elm's Cmd system is same-frame, so the optimization
  is unnecessary there. In Bubble Tea, Cmd execution adds 1 frame delay.

## Message-Based Communication

Components communicate with Terminal through messages, not by returning
result structs that Terminal reads:

```
ThemeSelector.Update     → tea.Cmd(ThemeSelectedMsg)  → Terminal.Update handles it
ModelSelector.Update     → tea.Cmd(ModelSelectedMsg)  → Terminal.Update handles it
HelpWindow.Update        → tea.Cmd(HelpCmdMsg)        → Terminal.Update handles it
AttachmentWindow.Update  → tea.Cmd(AttachmentSelectedMsg) → Terminal.Update handles it
ConfirmDialog.Update     → tea.Cmd(ConfirmResultMsg)  → Terminal.Update handles it
```

Terminal does NOT read component internals. It only handles messages
in its own Update switch.

## I/O Strategy

| I/O Operation | Path | Reason |
|--------------|------|--------|
| `emitCommand` (TLV write) | `tea.Cmd` | Always in Update context |
| `submitCmd` (batch TLV writes) | `tea.Cmd` | Multiple writes, one unit |
| `startMCPAuthFlow` (OAuth) | `tea.Cmd` | Blocking wait, must be async |
| `WriteError` (in Update) | `tea.Cmd` | Notification, can be deferred 1 frame |
| `WriteError` (in Init) | Direct write | Not in Update, cannot return Cmd |
| `StartCallbackServer` | Direct write in Update | Unavoidable — Cmd needs resultCh |

Principle: All I/O in Update goes through `tea.Cmd`. Exceptions are operations
that must happen synchronously because their result is needed before the Cmd
can be created (e.g., `StartCallbackServer` creates the channel that the Cmd
waits on).

## Concurrency Model

```
tea.Cmd        → go p.Send(cmd())              ← goroutine
tea.Batch(a,b) → go a(); go b()                ← goroutine per Cmd
tea.Sequence(a,b) → a(); b()                   ← event loop, no goroutine
```

- `tea.Batch` is for independent operations (no ordering needed)
- `tea.Sequence` is for dependent operations (e.g., Close before Quit)

## Remaining Differences from Elm

| Aspect | Pure Elm | Our Code | Acceptable? |
|--------|----------|----------|-------------|
| Cmd | Data (inspectable) | `func() Msg` (opaque) | Yes — Bubble Tea constraint |
| Same-frame Cmd | Yes (recursive before render) | No (render before exec) | Yes — BT limitation |
| Continuous UI | Cmd is fine (same-frame) | Pure `HandleKey` (bypass Cmd) | Yes — necessary optimization |
| Messages | Sum types, exhaustive | `interface{}` + type switch | Yes — Go limitation |
| Sub-components | `Cmd.map` for type-safe routing | Flat switch in Terminal | Yes — Go has no generics for this |
| Immutable syntax | Record update `{ x \| f = v }` | Field assignment on local copy | Yes — equivalent semantics |
