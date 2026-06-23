# TLV Samples

Sample TLV messages for `alayacore --rawio`.

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
AT  ← stdout  Assistant text (delta: \x00<id>\x00<content>)
AR  ← stdout  Assistant reasoning (delta: \x00<id>\x00<content>)
AF  ← stdout  Function/tool lifecycle (JSON)
UF  ← stdout  Function/tool result (JSON)
SM  ← stdout  System message (JSON: {"type":"...","data":{...}})
```

## Delta Messages (AT, AR)

AT and AR use NUL-delimited stream IDs for incremental streaming:

```
\x00<stream-id>\x00<content>
```

**Stream ID format:** `<historyCount>` or `<historyCount+blockIndex>` (e.g. `5`, `6`, `7`)

**Stream ID:**
- Same stream ID → content is a continuation of that stream
- Different stream ID → different stream (may be concurrent)
- No NUL prefix → plain text, no stream ID

**Index assignment:**
- Anthropic: uses the content block index from the API. Blocks can interleave
  (e.g. thinking[0], text[1], thinking[2], text[3], tool_use[4]) — each gets
  a unique sequential index regardless of type. Without the index, two
  reasoning blocks in the same step would be indistinguishable.
- OpenAI: reasoning blocks get index `0`, text blocks get index `1`, and
  tool calls get index `2+` (never more than one of each type per step).

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
Session writes → stdout:       AF {"id":"t1","name":"read_file"}
                               AF {"id":"t1","input":{"path":"main.go"}}
                               UF {"id":"t1","output":[{"text":"package main...","type":"text"}]}
                               AT \x00 5 \x00 Here's what main.go does...
                               SM {"type":"task","data":{"in_progress":false,"context":0,"queue_items":[]}}
```

## Example: Image Prompt Flow

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...
                               UI data:image/png;base64,...
                               UT "What's in these images?"
```

UI frames must precede the UT frame they belong to.

## Example: Media Prompt Flow

```
Adapter writes → stdin:        UI data:image/jpeg;base64,...
                               UA data:audio/mpeg;base64,...
                               UV data:video/mp4;base64,...
                               UD data:application/pdf;base64,...
                               UT "Analyze these files"
```

Media frames (UI, UA, UV, UD) must precede the UT frame they belong to.
Multiple media frames of different types can be combined in any order.

## Samples by Tool

### read_file

```
ut-read-file.bin               UT "Read the file main.go"
af-read-file-start.bin         AF {"id":"t1","name":"read_file"}
af-read-file-input.bin         AF {"id":"t1","input":{"path":"main.go"}}
af-read-file-input-range.bin   AF {"id":"t1","input":{"path":"main.go","start_line":10,"num_lines":20}}
uf-read-file-success.bin       UF {"id":"t1","output":[{"text":"package main...","type":"text"}]}
uf-read-file-failed.bin        UF {"id":"t1","output":[{"text":"file not found","type":"text"}],"is_error":true}
```

### write_file

```
ut-write-file.bin              UT "Write a hello world Go program to hello.go"
af-write-file-start.bin        AF {"id":"t3","name":"write_file"}
af-write-file-input.bin        AF {"id":"t3","input":{"content":"package main","path":"main.go"}}
uf-write-file-success.bin      UF {"id":"t3","output":[{"text":"File written successfully","type":"text"}]}
uf-write-file-failed.bin       UF {"id":"t3","output":[{"text":"permission denied","type":"text"}],"is_error":true}
```

### edit_file

```
ut-edit-file.bin               UT "Edit main.go to fix the greeting"
af-edit-file-start.bin         AF {"id":"t2","name":"edit_file"}
af-edit-file-input.bin         AF {"id":"t2","input":{"new_string":"fmt.Printf","old_string":"fmt.Println","path":"main.go"}}
uf-edit-file-success.bin       UF {"id":"t2","output":[{"text":"File edited successfully","type":"text"}]}
uf-edit-file-failed.bin        UF {"id":"t2","output":[{"text":"old_string not found","type":"text"}],"is_error":true}
```

### search_content

```
ut-search-content.bin          UT "Search for TODO in Go files"
af-search-content-start.bin    AF {"id":"t4","name":"search_content"}
af-search-content-input.bin    AF {"id":"t4","input":{"pattern":"TODO"}}
uf-search-content-success.bin  UF {"id":"t4","output":[{"text":"main.go:1:package main","type":"text"}]}
uf-search-content-failed.bin   UF {"id":"t4","output":[{"text":"invalid regex","type":"text"}],"is_error":true}
```

### execute_command

```
ut-execute-command.bin         UT "Run: ls -la"
af-execute-command-start.bin   AF {"id":"t5","name":"execute_command"}
af-execute-command-input.bin   AF {"id":"t5","input":{"command":"ls -la"}}
uf-execute-command-success.bin UF {"id":"t5","output":[{"text":"total 42...","type":"text"}]}
uf-execute-command-failed.bin  UF {"id":"t5","output":[{"text":"command not found","type":"text"}],"is_error":true}
```

### Text / Reasoning / System

```
ut-hello.bin                   UT "hello"
ut-empty.bin                   UT "" (length 0)
at-delta-hello.bin             AT \x00 5 \x00 Hello
at-delta-world.bin             AT \x00 5 \x00 world (same stream)
at-delta-new-step.bin          AT \x00 6 \x00 Next step (new stream)
at-plain.bin                   AT "plain text without stream id"
ar-delta.bin                   AR \x00 5 \x00 thinking...
ui-image.bin                   UI data:image/jpeg;base64,...
ui-image-url.bin               UI https://example-files.cnbj1.mi-fds.com/example-files/image/image_example.png
ua-audio-url.bin               UA https://example-files.cnbj1.mi-fds.com/example-files/audio/audio_example.wav
uv-video-url.bin               UV https://example-files.cnbj1.mi-fds.com/example-files/video/video_example.mp4
sm-message-version.bin         SM {"type":"version","data":{"message_version":8}}
sm-model-list.bin              SM {"type":"model_list","data":{"models":[{"id":0,"name":"Anthropic / Claude Haiku 4",...},{"id":4,"name":"DeepSeek / DeepSeek-V4 Flash",...}],"model_config_path":"..."}}
sm-model.bin                   SM {"type":"model","data":{"active_id":4,"active_name":"DeepSeek / DeepSeek-V4 Flash","context_limit":1000000}}
sm-theme-list.bin              SM {"type":"theme_list","data":{"themes":[{"name":"theme-dark",...},{"name":"theme-light",...}]}}
sm-theme.bin                   SM {"type":"theme","data":{"name":"theme-dark"}}
sm-reasoning.bin               SM {"type":"reasoning","data":{"level":2}}
sm-task-start.bin              SM {"type":"task","data":{"in_progress":true,"context":0,"queue_items":[]}}
sm-task-queued.bin             SM {"type":"task","data":{"in_progress":true,"context":0,"queue_items":[{"queue_id":"Q1","type":"prompt","content":"Read the file main.go","created_at":"..."},{"queue_id":"Q2","type":"command","content":":continue","created_at":"..."}]}}
sm-task-end.bin                SM {"type":"task","data":{"in_progress":false,"context":0,"queue_items":[]}}
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

## Generate Image Requests (Go)

```sh
go run misc/gen_tlv_request.go "question" image1.jpg [image2.jpg ...]
```
