# Adaptor Architecture

## Communication Pattern

All adaptors (terminal, plainio) communicate with the session through the
TLV stream protocol:

```
Adaptor → Session:  streamInput (ChanInput)  — user text, commands
Session → Adaptor:  Output (io.Writer)       — TLV-encoded events
```

This is the **only** runtime channel. The session never calls adaptor
methods, and the adaptor never calls session methods during normal operation.

## Exception: Theme Persistence

Theme is a terminal-only UI concern. The terminal adaptor reads and writes
the active theme name directly via `RuntimeManager`:

```go
// Read — opening theme selector
m.runtimeMgr.GetActiveTheme()

// Write — user selects a theme
m.runtimeMgr.SetActiveTheme(selectedTheme.Name)
```

This is intentionally **not** part of the TLV protocol because:

1. Only the terminal adaptor uses themes. The plainio adaptor has no visual
   rendering and would ignore theme data entirely.
2. Theme is persistent config (written to `runtime.conf`), not transient
   session state. Routing it through TLV would add protocol complexity for
   no benefit.
3. The `RuntimeManager` is already a narrow, read/write-only config store.
   The terminal holds a direct reference to it — no full `Session` coupling.

## Adaptor Bootstrap

The `adaptor.go` `Run()` function does access `Session` fields during
startup (before the Terminal is created):

- `session.InitError()` — fatal initialization check
- `session.ModelManager.GetLoadErrors()` — print config warnings
- `session.HasModels()` — abort if no models configured
- `session.GetRuntimeManager().GetActiveTheme()` — load initial theme

This is setup code, not runtime coupling. Once the Bubbletea program starts,
the Terminal struct only interacts with the session via TLV.
