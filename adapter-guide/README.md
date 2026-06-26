# Adapter Guide

This directory provides TLV protocol reference for `alayacore --rawio`.
The `tlv-samples/` subdirectory contains sample `.bin` frames; this document serves as an **adapter implementation guide** for developers building custom adapters.

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
AT  ← stdout  Assistant text (delta: \x00<id>\x00<content>)
AR  ← stdout  Assistant reasoning (delta: \x00<id>\x00<content>)
AF  ← stdout  Function/tool lifecycle (JSON)
UF  ← stdout  Function/tool result (JSON)
SM  ← stdout  System message (JSON: {"type":"...","data":{...}})
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
| `T` | **T**ext | UT, AT |
| `I` | **I**mage | UI |
| `V` | **V**ideo | UV |
| `A` | **A**udio | UA |
| `D` | **D**ocument | UD |
| `R` | **R**easoning | AR |
| `F` | **F**unction/tool | AF, UF |
| `E` | **E**nd (flush) | UE |
| `M` | **M**essage | SM |

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

## Delta Messages (AT, AR)

AT and AR use NUL-delimited history IDs for **incremental streaming**:

```
\x00<history-id>\x00<content>
```

The same history ID can appear multiple times — each subsequent frame is a
continuation (delta) of that content block. The adapter concatenates them.

**History ID format:** a flat monotonic history counter (e.g. `1`, `2`, `3`).
Each content block (text, reasoning, tool call, user content part) receives a
unique ID from this counter.

**History ID:**
- Same history ID → content is a continuation of that stream (AT/AR only)
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
| AT, AR | **History ID** — same ID = same stream (delta concatenation) |
| AF | **Tool call ID** (`"id"` field in JSON) — start and input frames for the same tool |
| UF | **Tool call ID** — matches the corresponding AF |
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

Tool execution uses a two-frame lifecycle for AF, with an `is_error` discriminator for UF:

**AF** — function lifecycle:
- `{"id":"t1","name":"read_file"}` — tool name announced (start frame, no input yet)
- `{"id":"t1","input":{...}}` — full tool arguments (input frame, name already known)

**UF** — function result:
- `{"id":"t1","output":[...]}` — succeeded (`is_error` is omitted when `false`)
- `{"id":"t1","output":[...],"is_error":true}` — failed

A tool call (AF) without a matching UF is still in progress. Each `.bin` sample below shows one frame in this lifecycle.

## Example: Text Prompt Flow

```
Adapter writes → stdin:        UT "Read the file main.go"
                               UE
Session writes → stdout:       UT \x00 1 \x00 Read the file main.go        ← echo with history ID
                               AF {"id":"t1","name":"read_file"}           ← non-user tag flushes user msg
                               AF {"id":"t1","input":{"path":"main.go"}}
                               UF {"id":"t1","output":[{"text":"package main...","type":"text"}]}
                               AT \x00 2 \x00 Here's what main.go does...
                               SM {"type":"task","data":{"in_progress":false,"context":0}}
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
                               AT \x00 4 \x00 These images contain...
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
                               SM {"type":"task","data":{"in_progress":false,"context":0}}
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

- AT/AR: Same history ID across successive frames → content continuation (delta)
- All tags: Different history ID → different content block
- History IDs are monotonic counters — they increase over the session lifetime
- History IDs are ephemeral: rebuilt from `seqID++` on session load, not persisted

## Samples by Tool

Each tool's samples list the stdin frames (adapter → agent) followed by
the stdout frames (agent → adapter). The first stdout frame carries the
user's prompt echo with its assigned history ID.

### read_file

```
stdin:  ut-read-file.bin               UT "Read the file main.go"
                                       UE
stdout: (echo)                         UT \x00 1 \x00 Read the file main.go
        af-read-file-start.bin         AF {"id":"t1","name":"read_file"}
        af-read-file-input.bin         AF {"id":"t1","input":{"path":"main.go"}}
        af-read-file-input-range.bin   AF {"id":"t1","input":{"path":"main.go","start_line":10,"num_lines":20}}
        uf-read-file-success.bin       UF {"id":"t1","output":[{"text":"package main...","type":"text"}]}
        uf-read-file-failed.bin        UF {"id":"t1","output":[{"text":"file not found","type":"text"}],"is_error":true}
```

### write_file

```
stdin:  ut-write-file.bin              UT "Write a hello world Go program to hello.go"
                                       UE
stdout: (echo)                         UT \x00 1 \x00 Write a hello world Go program to hello.go
        af-write-file-start.bin        AF {"id":"t3","name":"write_file"}
        af-write-file-input.bin        AF {"id":"t3","input":{"content":"package main","path":"main.go"}}
        uf-write-file-success.bin      UF {"id":"t3","output":[{"text":"File written successfully","type":"text"}]}
        uf-write-file-failed.bin       UF {"id":"t3","output":[{"text":"permission denied","type":"text"}],"is_error":true}
```

### edit_file

```
stdin:  ut-edit-file.bin               UT "Edit main.go to fix the greeting"
                                       UE
stdout: (echo)                         UT \x00 1 \x00 Edit main.go to fix the greeting
        af-edit-file-start.bin         AF {"id":"t2","name":"edit_file"}
        af-edit-file-input.bin         AF {"id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
        uf-edit-file-success.bin       UF {"id":"t2","output":[{"text":"File edited successfully","type":"text"}]}
        uf-edit-file-failed.bin        UF {"id":"t2","output":[{"text":"old_string not found","type":"text"}],"is_error":true}
```

### search_content

```
stdin:  ut-search-content.bin          UT "Search for TODO in Go files"
                                       UE
stdout: (echo)                         UT \x00 1 \x00 Search for TODO in Go files
        af-search-content-start.bin    AF {"id":"t4","name":"search_content"}
        af-search-content-input.bin    AF {"id":"t4","input":{"pattern":"TODO"}}
        uf-search-content-success.bin  UF {"id":"t4","output":[{"text":"main.go:1:package main","type":"text"}]}
        uf-search-content-failed.bin   UF {"id":"t4","output":[{"text":"invalid regex","type":"text"}],"is_error":true}
```

### execute_command

```
stdin:  ut-execute-command.bin         UT "Run: ls -la"
                                       UE
stdout: (echo)                         UT \x00 1 \x00 Run: ls -la
        af-execute-command-start.bin   AF {"id":"t5","name":"execute_command"}
        af-execute-command-input.bin   AF {"id":"t5","input":{"command":"ls -la"}}
        uf-execute-command-success.bin UF {"id":"t5","output":[{"text":"total 42...","type":"text"}]}
        uf-execute-command-failed.bin  UF {"id":"t5","output":[{"text":"command not found","type":"text"}],"is_error":true}
```

### Text / Reasoning / System

```
ut-hello.bin                   UT "hello"
ut-empty.bin                   UT "" (length 0)
at-delta-hello.bin             AT \x00 1 \x00 Hello
at-delta-world.bin             AT \x00 1 \x00 world (same stream)
at-delta-new-step.bin          AT \x00 2 \x00 Next step (new stream)
at-plain.bin                   AT "plain text without history id"
ar-delta.bin                   AR \x00 3 \x00 thinking...
ui-image.bin                   UI data:image/jpeg;base64,...
ui-image-url.bin               UI https://example-files.cnbj1.mi-fds.com/example-files/image/image_example.png
ua-audio-url.bin               UA https://example-files.cnbj1.mi-fds.com/example-files/audio/audio_example.wav
uv-video-url.bin               UV https://example-files.cnbj1.mi-fds.com/example-files/video/video_example.mp4
ut-model-sync.bin              UT ":model_sync [{\"id\":0,\"name\":\"Anthropic / Claude Haiku 4\",...}]    — sync edited model config"
sm-message-version.bin         SM {"type":"version","data":{"message_version":8}}
sm-model-list.bin              SM {"type":"model_list","data":{"models":[{"id":0,"name":"Anthropic / Claude Haiku 4",...},{"id":4,"name":"DeepSeek / DeepSeek-V4 Flash",...}]}}
sm-model.bin                   SM {"type":"model","data":{"active_id":4,"active_name":"DeepSeek / DeepSeek-V4 Flash","context_limit":1000000}}
sm-theme-list.bin              SM {"type":"theme_list","data":{"themes":[{"name":"theme-dark",...},{"name":"theme-light",...}]}}
sm-theme.bin                   SM {"type":"theme","data":{"name":"theme-dark"}}
sm-reasoning.bin               SM {"type":"reasoning","data":{"level":2}}
sm-video-config.bin            SM {"type":"video_config","data":{"fps":5,"res":1}}
sm-task-start.bin              SM {"type":"task","data":{"in_progress":true,"context":0}}
sm-task-end.bin                SM {"type":"task","data":{"in_progress":false,"context":0}}
sm-error.bin                   SM {"type":"error","data":{"text":"something broke"}}
sm-notify.bin                  SM {"type":"notify","data":{"text":"all good"}}
sm-tool-confirm.bin            SM {"type":"tool_confirm","data":{"id":"t1"}}
```

## Use

```sh
cat tlv-samples/ut-read-file.bin | alayacore --rawio
cat tlv-samples/ut-read-file.bin | alayacore --rawio | go run ./misc/tlvcat.go
cat tlv-samples/af-read-file-start.bin | go run ./misc/tlvcat.go
```

## Generate Media Requests (Go)

```sh
go run misc/gen_tlv_request.go "question" image.jpg audio.wav video.mp4
```
