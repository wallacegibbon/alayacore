# Adapter Guide

This document is a **dual-purpose reference**: it describes the TLV protocol
used by `alayacore --rawio` for **human adapter developers**, and it is also
designed to be read by **AI agents** that need to understand the protocol
in order to generate or update adapter implementations.

The `tlv-samples/` subdirectory contains sample `.bin` frames for reference.
When updating this guide, maintain the structured, unambiguous style so that
both human and AI readers can rely on it as the single source of truth.

## Wire Format

```
[2-byte tag][4-byte length (big-endian)][N bytes of value]
```

## Tags

```
UT  → stdin   User text
UI  → stdin   User image (data:image/...;base64,... or URL)
UV  → stdin   User video (data:video/...;base64,... or URL)
UA  → stdin   User audio (data:audio/...;base64,... or URL)
UD  → stdin   User document (data:application/...;base64,... or URL)
UE  → stdin   User message end — flushes staged content as a single message
At  ← stdout  Assistant text (streaming delta: \x00<id>\x00<content>)
Ar  ← stdout  Assistant reasoning (streaming delta: \x00<id>\x00<content>)
Af  ← stdout  Function/tool argument (streaming delta: \x00<id>\x00<JSON>)
AT  ← stdout  Assistant text (complete/authoritative: \x00<id>\x00<content>)
AR  ← stdout  Assistant reasoning (complete/authoritative: \x00<id>\x00<content>)
AF  ← stdout  Function/tool lifecycle (\x00<id>\x00<JSON>)
UF  ← stdout  Function/tool result (\x00<id>\x00<JSON>)
SM  ← stdout  System message, no history ID (JSON: {"type":"...","data":{...}})
UT  ← stdout  User text echo (\x00<id>\x00<content>)
UI  ← stdout  User image echo (\x00<id>\x00<data URI or URL>)
UV  ← stdout  User video echo (\x00<id>\x00<data URI or URL>)
UA  ← stdout  User audio echo (\x00<id>\x00<data URI or URL>)
UD  ← stdout  User document echo (\x00<id>\x00<data URI or URL>)
```

## Tag Naming Convention

Each tag is two characters: **role** + **type**.

| First letter | Role | Examples |
|---|---|---|
| `U` | **U**ser | UT (user text), UI (user image), UF (function result) |
| `A` | **A**ssistant | AT (assistant text), AR (assistant reasoning), AF (function call) |
| `S` | **S**ystem | SM (system message) |

| Second letter | Type | Examples |
|---|---|---|
| `T` | **T**ext | UT, AT, At |
| `I` | **I**mage | UI |
| `V` | **V**ideo | UV |
| `A` | **A**udio | UA |
| `D` | **D**ocument | UD |
| `R` | **R**easoning | AR, Ar |
| `F` | **F**unction/tool | AF, UF, Af |
| `E` | **E**nd (flush) | UE |
| `M` | **M**essage | SM |

**Case convention:** Uppercase tags carry complete/authoritative content.
Lowercase tags carry streaming delta (incremental) content:
- `At`/`Ar` — text/reasoning deltas, appended to the window per chunk
- `AT`/`AR` — complete text/reasoning, replaces the accumulated delta content (useful for replay)
- `Af` — tool argument delta, partial JSON chunk during streaming
- `AF` — complete tool call (start + input frames)

The role indicates who the content belongs to in the conversation, **not** the
direction of the message. For example, UF is a function result (user-role
content) but appears on stdout. Similarly, UT/UI/UV/UA/UD on stdout are user
message echoes — they carry user-role content sent from the agent back to the
adapter.

**Important:** User tags (UT, UI, UV, UA, UD) appear on **both** stdin and stdout:

| Direction | Meaning |
|-----------|---------|
| **stdin** (adapter → agent) | New user input — the adapter sends a user message to the agent |
| **stdout** (agent → adapter) | User message echo — the agent echoes the user's message back to the adapter for display, with a history ID assigned |

The adapter must be prepared to **receive** user tags on stdout in these scenarios:

1. **Prompt echo** — When the user sends a prompt (UT + UE on stdin), the agent echoes each content part back on stdout with an assigned history ID before sending them to the LLM.
2. **Session replay** — When a saved session file (key-value frontmatter + binary TLV body, specified via `--session`) is loaded, all historical content (including user messages) is replayed to the adapter on stdout with their original history IDs.
3. **Auto-summarize** — The auto-summarization prompt is echoed as UT on stdout.

> **For adapter implementors:** You cannot assume user tags only appear on stdin.
> Both the terminal adapter (`internal/adapters/terminal/output.go`) and the plainio
> adapter (`internal/adapters/plainio/output.go`) handle user tags from stdout.

## Delta Messages (At, Ar)

Streaming content uses **lowercase** tags (`At`, `Ar`) with NUL-delimited history IDs:

```
\x00<history-id>\x00<content>
```

The same history ID can appear multiple times — each subsequent frame is a
continuation (delta) of that content block. The adapter concatenates them.

**Complete/authoritative frames** use **uppercase** tags (`AT`, `AR`). These are
sent after all deltas for a content block have been received. During live
streaming the adapter already has the content from the deltas and may skip the
complete frame. During replay (session load) only the uppercase frames are sent;
no deltas precede them.

**History ID format:** a flat monotonic history counter (e.g. `1`, `2`, `3`).
Each content block (text, reasoning, tool call, user content part) receives a
unique ID from this counter.

**History ID:**
- Same history ID → content is a continuation of that stream (At/Ar only)
- Different history ID → different content block
- No NUL prefix → plain text, no history ID

## User Echo on stdout (UT, UI, UV, UA, UD)

User echoes are **not** delta — each frame contains **complete content** (the full
text string, the full data URI or URL). There is no streaming/continuation for
user echo frames; each frame is a standalone content part.

When a user sends multiple parts in one prompt (e.g. image + text), the agent
echoes them as consecutive user-tag frames on stdout. The adapter groups them
into a single user message. Other tag types use different grouping mechanisms:

| Tags | Grouping mechanism |
|------|-------------------|
| UT, UI, UV, UA, UD (stdout) | **Position** — consecutive frames belong to one message; a non-user tag starts the next |
| At, Ar | **History ID** — same ID = same stream (delta concatenation) |
| Af | **JSON `"id"`** — partial JSON chunk for the named tool call |
| AF | **History ID** (start+input share same ID) + **JSON `"id"`** — start announces name, input carries arguments |
| UF | **JSON `"id"`** — matches the corresponding AF (history ID is present but not used for matching) |
| SM | **None** — each frame is standalone |

Each user content part gets its own **unique** history ID (monotonic counter).

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...
                               UI data:image/png;base64,...
                               UT "What's in these images?"
                               UE
Session writes → stdout:       UI \x00 1 \x00 data:image/jpeg;base64,...
                               UI \x00 2 \x00 data:image/png;base64,...    ← different ID
                               UT \x00 3 \x00 What's in these images?      ← different ID
                               AT \x00 4 \x00 These images show...         ← starts assistant message
```

## Function Lifecycle (AF, UF)

All stdout content frames carry a NUL-delimited history ID (`\x00<id>\x00`). For
AF/UF the history ID identifies the content block; tool matching uses the JSON
`"id"` field.

**AF** — function lifecycle (two or three frames live, one frame on replay):
- `\x00<id>\x00{"id":"t1","name":"read_file"}` — tool name announced (start frame, no input yet)
- `\x00<id>\x00{"id":"t1","delta":"{\\"path\\":"}"` — partial JSON argument (delta frame, zero or more during streaming)
- `\x00<id>\x00{"id":"t1","input":{...}}` — full tool arguments (input frame, name already known, same history ID as start)

During live streaming `OnToolInputStart` → `AF` start, `OnToolInputDelta` → `Af` (zero or more),
and `OnToolInputComplete` → `AF` input share the same history ID (they belong to the same tool call).
During session replay the tool call is a single AF frame with both `name` and `input`.

**UF** — function result:
- `\x00<id>\x00{"id":"t1","output":[...]}` — succeeded (`is_error` omitted when `false`)
- `\x00<id>\x00{"id":"t1","output":[...],"is_error":true}` — failed

A tool call (AF) without a matching UF is still in progress. Each `.bin` sample below shows one frame in this lifecycle.

## Example: Text Prompt Flow (with streaming deltas)

```
Adapter writes → stdin:        UT "Read the file main.go"
                               UE
Session writes → stdout:       UT \x00 1 \x00 Read the file main.go        ← echo with history ID
                               AF \x00 2 \x00 {"id":"t1","name":"read_file"} ← non-user tag flushes user msg
                               Af \x00 2 \x00 {"id":"t1","delta":"{\\"path\\":"} ← partial arg delta
                               Af \x00 2 \x00 {"id":"t1","delta":"\\"main.go\\"}"}  ← more args
                               AF \x00 2 \x00 {"id":"t1","input":{"path":"main.go"}}  ← complete args
                               UF \x00 3 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
                               At \x00 4 \x00 Here's what main.go does...    ← streaming text delta
                               AT \x00 4 \x00 Here's what main.go does...    ← authoritative complete
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

## Example: Text Prompt Flow (replay, no deltas)

```
Session writes → stdout:       UT \x00 1 \x00 Read the file main.go
                               AF \x00 2 \x00 {"id":"t1","name":"read_file","input":{"path":"main.go"}}
                               UF \x00 3 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
                               AT \x00 4 \x00 Here's what main.go does...
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

## Example: Image Prompt Flow

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...
                               UI data:image/png;base64,...
                               UT "What's in these images?"
                               UE
Session writes → stdout:       UI \x00 1 \x00 data:image/jpeg;base64,...
                               UI \x00 2 \x00 data:image/png;base64,...
                               UT \x00 3 \x00 What's in these images?
                               AT \x00 4 \x00 These images contain...         ← complete (live or replay)
```

The three user frames (IDs 1, 2, 3) are accumulated into one user message.
`AT \x00 4` triggers the flush and begins the assistant response.

## Example: Media Prompt Flow

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...    (or URL)
                               UA data:audio/mpeg;base64,...    (or URL)
                               UV data:video/mp4;base64,...     (or URL)
                               UD data:application/pdf;base64,... (or URL)
                               UT "Analyze these files"        (optional — media-only is valid)
                               UE
Session writes → stdout:       UI \x00 1 \x00 data:image/jpeg;base64,...
                               UA \x00 2 \x00 data:audio/mpeg;base64,...
                               UV \x00 3 \x00 data:video/mp4;base64,...
                               UD \x00 4 \x00 data:application/pdf;base64,...
                               UT \x00 5 \x00 Analyze these files       (absent for media-only)
                               AT \x00 6 \x00 Here's my analysis...
```

All media types accept either `data:` URIs or plain URLs. Media frames and text
can be combined in any order. The adapter accumulates them all until the first
non-user tag.

## Example: Session Replay Flow

When a saved session is loaded, all previous content is replayed on stdout.
Each content part gets a sequential history ID (rebuilt from `seqID++`):

```
Session writes → stdout:       UT \x00 1 \x00 Hello                        ← user
                               AT \x00 2 \x00 Hi! How can I help?          ← assistant
                               UT \x00 3 \x00 Read main.go                 ← user
                               AF \x00 4 \x00 {"id":"t1","name":"read_file","input":{"path":"main.go"}}
                               UF \x00 5 \x00 {"id":"t1","output":[...]}
                               AT \x00 6 \x00 Here's the content...        ← assistant
                               SM {"type":"task","data":{"in_progress":false,"context":1500}}
```

Note: During replay, a tool call is a single AF frame with both `name` and
`input` together (one `ToolInputPart`). The two-frame split (start then input)
only happens during live streaming. User and assistant frames are interleaved
as they were in the original conversation.

## Adapter Implementation Notes

### Handling user tags on stdout

1. **Parse the history ID** from the NUL-delimited prefix (`\x00<id>\x00<content>`).
   If no NUL prefix, the content is plain text without a history ID.

2. **Group by position:** consecutive user-tag frames (UT, UI, UV, UA, UD)
   belong to one user message. When a non-user tag arrives (AT, AR, AF, UF, SM),
   it starts the next message. There is no `UE` on stdout.

3. **Display:** Since there is no UE on stdout, the adapter cannot know if
   more user frames are coming until a non-user tag arrives. Render each
   frame incrementally as it arrives (e.g. the TUI adapter calls
   `SetWindowVisible` + `dirty.Store(true)` on every frame, then finalizes
   the window metadata when a non-user tag arrives).

### Handling history IDs

All stdout content-part frames (AT, AR, AF, UF, UT/UI/UA/UV/UD echoes) carry
a NUL-delimited history ID prefix `\x00<id>\x00`. The history ID is a flat
monotonic counter that increases over the session lifetime. System messages
(SM) and stdin frames never carry history IDs.

The **semantics** of the history ID differ by tag type:

| Tag | Same history ID means | Grouping / matching |
|-----|-----------------------|---------------------|
| AT, AR | **Content continuation** (delta) — concatenate frames with the same ID into one block | History ID |
| AF (start + input) | **Same tool call** — start frame announces the name, input frame carries arguments (not concatenated) | JSON `"id"` field + history ID |
| UF | **Same tool result** (matched to AF by JSON `"id"`, not by history ID) | JSON `"id"` field |
| UT/UI/UA/UV/UD (stdout) | **N/A** — each echo has a unique history ID | **Position** (consecutive user tags → one message) |

**Key rules:**
- Different history ID → different content block (all tags)
- No NUL prefix → plain text, no history ID (stdout: `at-plain.bin`, SM; stdin: all frames)
- History IDs are ephemeral: rebuilt from `seqID++` on session load, not persisted
- On session replay, ALL content parts (AT, AR, AF, UF, UT/UI/UA/UV/UD) carry a history ID,
  matching the format they had during live streaming

### Error handling

1. **Corrupt session file**: If a content part in a saved session cannot be
   deserialized (unknown tag, malformed JSON), the session load fails and the
   agent sends an error message like:
   ```
   SM {"type":"error","data":{"text":"corrupt session file: failed to serialize content part (HistoryID=5): unexpected end of JSON input"}}
   ```
   The adapter should display the error and stop sending new prompts — no
   further frames will be processed.

2. **Model config errors**: At startup and after `:model_load`, model
   configuration errors (invalid fields, duplicate names, etc.) are sent to the
   adapter as system error messages:
   ```
   SM {"type":"error","data":{"text":"model \"Bad Model\": unknown protocol_type \"foobar\" — skipped"}}
   SM {"type":"error","data":{"text":"model block 3: duplicate name \"Model A\" — skipped"}}
   ```
   These are informational — the session continues with whatever valid models
   are available. The adapter should display them so the user can fix their
   `model.conf`.

3. **MCP config errors**: At startup, MCP configuration errors (empty server
   name, duplicate server names, etc.) are sent to the adapter as system error
   messages:
   ```
   SM {"type":"error","data":{"text":"mcp.conf: skipping block with empty server name"}}
   SM {"type":"error","data":{"text":"mcp.conf: duplicate server name \"my-db\" — skipped"}}
   ```
   These are informational — the session continues with whatever valid MCP
   servers are available. The adapter should display them so the user can fix
   their `mcp.conf`.

4. **Output stream broken**: On the first write error to stdout, the agent
   cancels the session context and stops processing. No further frames are
   sent. The adapter will see EOF on stdout and should handle it gracefully
   (e.g. close the connection, show a notification).

5. **Missing UF**: A tool call (AF) without a matching UF is still in progress.
   If the session ends before all tool calls complete, pending tool calls are
   abandoned — no UF will arrive for them.

5. **History ID collision**: History IDs are assigned monotonically. During
   live streaming they come from a single counter; during replay they are
   rebuilt from `seqID++`. Collisions cannot occur under normal operation.
   If a corrupt session causes duplicate IDs, the adapter should treat each
   frame as an independent content block (AT/AR delta concatenation on same
   ID still applies).

## Samples by Tool

Each tool's samples list the stdin frames (adapter → agent) followed by
the stdout frames (agent → adapter). The first stdout frame carries the
user's prompt echo with its assigned history ID.

### read_file

```
stdin:  ut-read-file.bin               UT "Read the file main.go"
                                       UE
stdout: ut-echo-read-file.bin          UT \x00 5 \x00 Read the file main.go
        af-read-file-start.bin         AF \x00 6 \x00 {"id":"t1","name":"read_file"}
        af-read-file-input.bin         AF \x00 6 \x00 {"id":"t1","input":{"path":"main.go"}}   ← or af-read-file-input-range.bin
        uf-read-file-success.bin       UF \x00 7 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
        uf-read-file-failed.bin        UF \x00 7 \x00 {"id":"t1","output":[{"text":"file not found","type":"text"}],"is_error":true}
```

The `af-read-file-input-range.bin` sample is an **alternative** to `af-read-file-input.bin` — it demonstrates the `start_line` and `num_lines` optional parameters. Only one input frame is sent per tool invocation.

### write_file

```
stdin:  ut-write-file.bin              UT "Write a hello world Go program to hello.go"
                                       UE
stdout: (echo)                         UT \x00 9 \x00 Write a hello world Go program to hello.go
        af-write-file-start.bin        AF \x00 10 \x00 {"id":"t3","name":"write_file"}
        af-write-file-input.bin        AF \x00 10 \x00 {"id":"t3","input":{"content":"package main","path":"main.go"}}
        uf-write-file-success.bin      UF \x00 11 \x00 {"id":"t3","output":[{"text":"File written successfully","type":"text"}]}
        uf-write-file-failed.bin       UF \x00 11 \x00 {"id":"t3","output":[{"text":"permission denied","type":"text"}],"is_error":true}
```

### edit_file

```
stdin:  ut-edit-file.bin               UT "Edit main.go to fix the greeting"
                                       UE
stdout: (echo)                         UT \x00 7 \x00 Edit main.go to fix the greeting
        af-edit-file-start.bin         AF \x00 8 \x00 {"id":"t2","name":"edit_file"}
        af-edit-file-input.bin         AF \x00 8 \x00 {"id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
        uf-edit-file-success.bin       UF \x00 9 \x00 {"id":"t2","output":[{"text":"File edited successfully","type":"text"}]}
        uf-edit-file-failed.bin        UF \x00 9 \x00 {"id":"t2","output":[{"text":"old_string not found","type":"text"}],"is_error":true}
```

### search_content

```
stdin:  ut-search-content.bin          UT "Search for TODO in Go files"
                                       UE
stdout: (echo)                         UT \x00 11 \x00 Search for TODO in Go files
        af-search-content-start.bin    AF \x00 12 \x00 {"id":"t4","name":"search_content"}
        af-search-content-input.bin    AF \x00 12 \x00 {"id":"t4","input":{"pattern":"TODO"}}
        uf-search-content-success.bin  UF \x00 13 \x00 {"id":"t4","output":[{"text":"main.go:1:package main","type":"text"}]}
        uf-search-content-failed.bin   UF \x00 13 \x00 {"id":"t4","output":[{"text":"invalid regex","type":"text"}],"is_error":true}
```

### execute_command

```
stdin:  ut-execute-command.bin         UT "Run: ls -la"
                                       UE
stdout: (echo)                         UT \x00 13 \x00 Run: ls -la
        af-execute-command-start.bin   AF \x00 14 \x00 {"id":"t5","name":"execute_command"}
        af-execute-command-input.bin   AF \x00 14 \x00 {"id":"t5","input":{"command":"ls -la"}}
        uf-execute-command-success.bin UF \x00 15 \x00 {"id":"t5","output":[{"text":"total 42...","type":"text"}]}
        uf-execute-command-failed.bin  UF \x00 15 \x00 {"id":"t5","output":[{"text":"command not found","type":"text"}],"is_error":true}
```

### Text / Reasoning / System / Tool

All stdout frames except SM carry a NUL-delimited history ID prefix.
Samples are grouped by direction below.

**stdin (adapter → agent, no history ID):**

```
ut-hello.bin                   UT "hello"
ut-empty.bin                   UT "" (length 0)
ui-image.bin                   UI data:image/jpeg;base64,...
ui-image-url.bin               UI https://...
ua-audio.bin                   UA data:audio/mpeg;base64,...
ua-audio-url.bin               UA https://...
uv-video.bin                   UV data:video/mp4;base64,...
uv-video-url.bin               UV https://...
ud-document.bin                UD data:application/pdf;base64,...
ue.bin                         UE "" (length 0)
ut-model-sync.bin              UT ":model_sync [{id,name,protocol_type,base_url,api_key,model_name,context_limit,max_tokens},...]"
```

**stdout — delta/echo (with history ID `\x00<id>\x00`):**

```
ut-echo-hello.bin              UT \x00 1 \x00 Hello
ut-echo-read-file.bin          UT \x00 5 \x00 Read the file main.go
ui-echo-image.bin              UI \x00 2 \x00 data:image/jpeg;...
ua-echo-audio-url.bin          UA \x00 3 \x00 https://...
uv-echo-video-url.bin          UV \x00 4 \x00 https://...
at-delta-hello.bin             AT \x00 1 \x00 Hello
at-delta-world.bin             AT \x00 1 \x00 world (same stream)
at-delta-new-step.bin          AT \x00 2 \x00 Next step (new stream)
at-plain.bin                   AT "plain text without history id"   ← special case: no NUL prefix, no delta
ar-delta.bin                   AR \x00 3 \x00 thinking...
```

**stdout — tool frames (with history ID `\x00<id>\x00`, JSON `"id"` for matching):**

```
af-read-file-start.bin         AF \x00 6 \x00 {"id":"t1","name":"read_file"}
af-read-file-input.bin         AF \x00 6 \x00 {"id":"t1","input":{"path":"main.go"}}
af-read-file-input-range.bin   AF \x00 6 \x00 {"id":"t1","input":{"path":"main.go","start_line":10,"num_lines":20}}
uf-read-file-success.bin       UF \x00 7 \x00 {"id":"t1","output":[{"text":"package main...","type":"text"}]}
uf-read-file-failed.bin        UF \x00 7 \x00 {"id":"t1","output":[{"text":"file not found","type":"text"}],"is_error":true}
af-edit-file-start.bin         AF \x00 8 \x00 {"id":"t2","name":"edit_file"}
af-edit-file-input.bin         AF \x00 8 \x00 {"id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
uf-edit-file-success.bin       UF \x00 9 \x00 {"id":"t2","output":[{"text":"File edited successfully","type":"text"}]}
uf-edit-file-failed.bin        UF \x00 9 \x00 {"id":"t2","output":[{"text":"old_string not found","type":"text"}],"is_error":true}
af-write-file-start.bin        AF \x00 10 \x00 {"id":"t3","name":"write_file"}
af-write-file-input.bin        AF \x00 10 \x00 {"id":"t3","input":{"content":"package main","path":"main.go"}}
uf-write-file-success.bin      UF \x00 11 \x00 {"id":"t3","output":[{"text":"File written successfully","type":"text"}]}
uf-write-file-failed.bin       UF \x00 11 \x00 {"id":"t3","output":[{"text":"permission denied","type":"text"}],"is_error":true}
af-search-content-start.bin    AF \x00 12 \x00 {"id":"t4","name":"search_content"}
af-search-content-input.bin    AF \x00 12 \x00 {"id":"t4","input":{"pattern":"TODO"}}
uf-search-content-success.bin  UF \x00 13 \x00 {"id":"t4","output":[{"text":"main.go:1:package main","type":"text"}]}
uf-search-content-failed.bin   UF \x00 13 \x00 {"id":"t4","output":[{"text":"invalid regex","type":"text"}],"is_error":true}
af-execute-command-start.bin   AF \x00 14 \x00 {"id":"t5","name":"execute_command"}
af-execute-command-input.bin   AF \x00 14 \x00 {"id":"t5","input":{"command":"ls -la"}}
uf-execute-command-success.bin UF \x00 15 \x00 {"id":"t5","output":[{"text":"total 42...","type":"text"}]}
uf-execute-command-failed.bin  UF \x00 15 \x00 {"id":"t5","output":[{"text":"command not found","type":"text"}],"is_error":true}
```

**stdout — system messages (no history ID, JSON `{"type":"...","data":{...}}`):**

| Type | JSON Schema (data fields) | Example `.bin` |
|------|--------------------------|----------------|
| `version` | `message_version` (int), `core_version` (string) | `sm-message-version.bin` |
| `model` | `active_id` (int), `active_name` (string), `context_limit` (int) | `sm-model.bin` |
| `model_list` | `models` (array of `{id:int, name:string, protocol_type:string, base_url:string, api_key:string, model_name:string, context_limit:int, max_tokens:int}`) | `sm-model-list.bin` |
| `theme` | `name` (string), `theme` (object, optional — full palette sent on startup, omitted on theme switch) | `sm-theme.bin` |
| `theme_list` | `themes` (array of `{name:string, theme:{primary, dim, muted, text, warning, error, success, selection, cursor, added, removed, fold_indicator: string}}`) | `sm-theme-list.bin` |
| `reasoning` | `level` (int: 0=off, 1=normal, 2=max) | `sm-reasoning.bin` |
| `video_config` | `fps` (int), `res` (int) | `sm-video-config.bin` |
| `task` | `in_progress` (bool), `current_step` (int, opt), `max_steps` (int, opt), `context` (int), `task_error` (bool, opt) | `sm-task-start.bin`, `sm-task-end.bin` |
| `error` | `text` (string) | `sm-error.bin` |
| `notify` | `text` (string) | `sm-notify.bin` |
| `tool_confirm` | `id` (string), `allowed` (bool, opt — present only in adapter→agent response) | `sm-tool-confirm.bin` |
| `mcp` | `status` (string: one of `connecting`, `auth_confirm`, `auth_running`, `connected`, `failed`, `done`), `server` (string, opt), `url` (string, opt — set for `auth_confirm`; may contain `{{redirect_uri}}` and `{{state}}` placeholders), `error` (string, opt — set for `failed`) | `sm-mcp-connecting.bin`, `sm-mcp-auth-confirm.bin`, `sm-mcp-auth-running.bin`, `sm-mcp-connected.bin`, `sm-mcp-failed.bin`, `sm-mcp-done.bin` |

Complete wire values:

```
sm-message-version.bin         {"type":"version","data":{"message_version":9,"core_version":"(set at build time)"}}
sm-model.bin                   {"type":"model","data":{"active_id":4,"active_name":"DeepSeek / DeepSeek-V4 Flash","context_limit":1000000}}
sm-model-list.bin              {"type":"model_list","data":{"models":[{"id":0,"name":"Anthropic / Claude Haiku 4","protocol_type":"anthropic","base_url":"https://api.anthropic.com","api_key":"sk-ant-...","model_name":"claude-haiku-4-20260515","context_limit":200000,"max_tokens":0},{"id":4,"name":"DeepSeek / DeepSeek-V4 Flash","protocol_type":"openai","base_url":"https://api.deepseek.com/v1","api_key":"sk-ds-...","model_name":"deepseek-v4-flash","context_limit":1000000,"max_tokens":0}]}}
sm-theme.bin                   {"type":"theme","data":{"name":"theme-dark"}}
sm-theme-list.bin              {"type":"theme_list","data":{"themes":[{"name":"theme-dark","theme":{"primary":"#89d4fa","dim":"#313244","muted":"#6c7086","text":"#cdd6f4","warning":"#f9e2af","error":"#f38ba8","success":"#a6e3a1","selection":"#fab387","cursor":"#cdd6f4","added":"#a6e3a1","removed":"#f38ba8","fold_indicator":"⁝"}},{"name":"theme-light","theme":{"primary":"#1e66f5","dim":"#ccd0da","muted":"#9ca0b0","text":"#4c4f69","warning":"#df8e1d","error":"#d20f39","success":"#40a02b","selection":"#fe640b","cursor":"#dc8a78","added":"#40a02b","removed":"#d20f39","fold_indicator":"⁝"}}]}}
sm-reasoning.bin               {"type":"reasoning","data":{"level":2}}
sm-video-config.bin            {"type":"video_config","data":{"fps":5,"res":1}}
sm-task-start.bin              {"type":"task","data":{"in_progress":true,"current_step":1,"max_steps":10,"context":0}}
sm-task-end.bin                {"type":"task","data":{"in_progress":false,"context":1500}}
sm-error.bin                   {"type":"error","data":{"text":"something broke"}}
sm-notify.bin                  {"type":"notify","data":{"text":"all good"}}
sm-tool-confirm.bin            {"type":"tool_confirm","data":{"id":"t1"}}
sm-mcp-connecting.bin          {"type":"mcp","data":{"status":"connecting","server":"github"}}
sm-mcp-auth-confirm.bin        {"type":"mcp","data":{"status":"auth_confirm","server":"github","url":"https://github.com/login/oauth/authorize?...redirect_uri={{redirect_uri}}&state={{state}}"}}
sm-mcp-auth-running.bin        {"type":"mcp","data":{"status":"auth_running","server":"github"}}
sm-mcp-connected.bin           {"type":"mcp","data":{"status":"connected","server":"github"}}
sm-mcp-failed.bin              {"type":"mcp","data":{"status":"failed","server":"github","error":"connection timeout"}}
sm-mcp-done.bin                {"type":"mcp","data":{"status":"done"}}
```

## Use

```sh
# Pipe a user prompt to the agent
cat tlv-samples/ut-read-file.bin | alayacore --rawio

# Pipe a user prompt and decode the response frames
cat tlv-samples/ut-read-file.bin | alayacore --rawio | go run ./misc/tlvcat.go

# Decode a single frame (stdin or stdout direction)
cat tlv-samples/af-read-file-start.bin | go run ./misc/tlvcat.go

# Decode a user echo frame (agent → adapter, with history ID)
cat tlv-samples/ut-echo-hello.bin | go run ./misc/tlvcat.go
```

## Generate Media Requests (Go)

```sh
go run misc/gen_tlv_request.go "question" image.jpg audio.wav video.mp4
```
