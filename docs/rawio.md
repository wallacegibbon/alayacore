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

- Stdin must contain valid TLV-encoded messages (2-byte tag, 4-byte
  length, value bytes).
- Each frame is read and forwarded to the session.
- **Ctrl-D** (EOF): closes stdin, waits for queued tasks to finish,
  exits with code `0`.
- **Ctrl-C** (SIGINT): closes stdin, waits for the current task to finish,
  exits with code `130` (128+SIGINT).
- Errors during the session cause the process to exit with code `1`.

## Output

- All TLV-encoded messages from the session are written directly to
  stdout with no formatting, interpretation, or filtering.
- Stderr is reserved for error messages, logging, and diagnostics.

## Example

```sh
# Send 2 TU (TagTextUser) frames to AlayaCore
printf 'TU\x00\x00\x00\x05helloTU\x00\x00\x00\x06my os?' | alayacore --rawio
```

## Use Cases

- **Orchestration**: A parent AI agent launches AlayaCore as a subprocess
  and communicates with it using TLV frames.
- **Testing**: Send specific TLV sequences to test session behavior.
- **Integration**: Connect AlayaCore to custom UIs, dashboards, or
  pipelines that speak the TLV protocol.
