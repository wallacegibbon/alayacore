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

Theme is a terminal-only UI concern. The session persists the active theme
name via `RuntimeManager`, and communicates it to the terminal adaptor through
the TLV protocol as part of `SystemInfo.ActiveTheme` (sent via `TagSystemData`):

```
User selects theme → :theme_set command → Session →
  RuntimeManager.SetActiveTheme()          ← persists to runtime.conf
  sendSystemInfo() with ActiveTheme set    ← sent to adaptor via TLV
    → Terminal reads ActiveTheme from StatusSnapshot
```

This is intentionally **not** a direct adaptor-to-RuntimeManager call because:

1. Only the terminal adaptor uses themes. The plainio adaptor has no visual
   rendering and would ignore theme data entirely.
2. Theme is persistent config (written to `runtime.conf`), not transient
   session state. Routing it through the session's TLV output ensures a
   single authority over runtime config.
3. The `RuntimeManager` stays inside the Session, where it belongs — the
   adaptor never holds a reference to it.

On startup, the terminal adaptor reads the initial theme from the first
`TagSystemData` message sent by `Session.sendSystemInfo()` during session
initialization. If no theme is set (first run), it defaults to `"theme-dark"`.

## Adaptor Bootstrap

The `StartSession()` function in `app/session.go` handles shared initialization
for all adaptors:

- `session.InitError()` — fatal initialization check (--model flag validation)
- `session.ModelManager.GetLoadErrors()` — print config warnings
- `session.HasModels()` — abort if no models configured

This is setup code, not runtime coupling. Once the Bubble Tea program starts,
the Terminal struct only interacts with the session via TLV.
