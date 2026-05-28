# Raw IO Mode

`--rawio` runs AlayaCore in raw TLV mode, where stdin and stdout carry
TLV-encoded messages directly. Unlike `--plainio` which translates between
human-readable text and TLV, rawio passes TLV frames through unmodified.

This mode is designed for **programmatic control** — a parent process
(e.g., another AI agent, a script, or an orchestration tool) sends TLV
frames to AlayaCore's stdin and reads TLV frames from its stdout, using
the TLV protocol directly.

## TLV Protocol

Messages use the standard Tag-Length-Value format:

```
[2-byte tag][4-byte length (big-endian)][value bytes]
```

See [TLV Protocol](adaptor-architecture.md#tlv-protocol) for full details.

## Input

- Raw bytes from stdin are piped directly to the session. The session's
  `io.ReadFull` handles TLV frame boundary detection internally.
- **Ctrl-D** (EOF): closes stdin, the session finishes queued tasks, and
  the process exits with code `0`.
- **Ctrl-C** (SIGINT): closes stdin. The current task finishes before
  exit. A second Ctrl-C forces immediate exit.

## Output

- All TLV-encoded messages from the session are written directly to
  stdout with no formatting, interpretation, or filtering.
- The controlling process is responsible for parsing TLV frames from
  stdout and handling any SE (system error) tags itself.
- Stderr is reserved for error messages, logging, and diagnostics.

## Errors

Rawio does not inspect or interpret TLV frames. If the session encounters
an error, it emits an SE (TagSystemError) frame on stdout — the
controlling process detects and handles it. The adaptor itself always
exits with code `0` (or `1` on startup failure, e.g. no models
configured).

## Example

```sh
# Send 2 UT (TagTextUser) frames to AlayaCore
printf 'UT\x00\x00\x00\x05helloUT\x00\x00\x00\x06my os?' | alayacore --rawio
```

## Use Cases

- **Orchestration**: A parent AI agent launches AlayaCore as a subprocess
  and communicates with it using TLV frames.
- **Testing**: Send specific TLV sequences to test session behavior.
- **Integration**: Connect AlayaCore to custom UIs, dashboards, or
  pipelines that speak the TLV protocol.
