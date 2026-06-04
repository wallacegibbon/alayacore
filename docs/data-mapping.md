# Provider Wire Format → Domain Type Mapping

How OpenAI and Anthropic API wire formats map to the domain types in `llm/types.go`.

## Domain Layer Overview

All providers eat different wire formats and emit the same domain types:

```go
// llm/types.go — the domain types

type ContentPart interface { isContentPart() }

// Implementations:
type TextPart      struct { Text string }
type ReasoningPart struct { Text string }
type ToolUsePart   struct { ID, ToolName string; Input json.RawMessage }
type ToolResultPart struct { ID string; Output ToolResultOutput }

type Message struct {
    Role    MessageRole   // "system" | "user" | "assistant" | "tool"
    Content []ContentPart
}

type StreamEvent interface { isStreamEvent() }

// Implementations:
type TextDeltaEvent      struct { Delta string }
type ReasoningDeltaEvent struct { Delta string }
type ToolUseStartEvent   struct { ID, ToolName string }
type ToolUsePart         struct { ID, ToolName string; Input json.RawMessage } // also a StreamEvent
type StepCompleteEvent   struct { Message; Usage; StopReason string }
```

## Design: Domain Layer Models Anthropic, Not OpenAI

The domain layer `Message.Content []ContentPart` is practically a **generic version** of Anthropic's `[]anthropicContentBlock` array, **not** OpenAI's flat-field model.

Compare the three representations for the same assistant message:

```go
// Domain (llm/types.go) — array of ContentPart interfaces
Message{
    Role: "assistant",
    Content: []ContentPart{
        ReasoningPart{Type:"reasoning", Text:"Let me think..."},
        TextPart{Type:"text", Text:"The answer is 42"},
        ToolUsePart{ID:"call_abc", ToolName:"read_file",
                     Input: json.RawMessage(`{"path":"/tmp/foo"}`)},
    },
}
```

```go
// Anthropic wire (anthropic.go) — array of concrete blocks, nearly 1:1
anthropicMessage{
    Role: "assistant",
    Content: []anthropicContentBlock{
        {Type:"thinking", Thinking: &"Let me think..."},
        {Type:"text", Text: "The answer is 42"},
        {Type:"tool_use", ID:"call_abc", Name:"read_file", Input: {"path":"/tmp/foo"}},
    },
}
```

```go
// OpenAI wire (openai.go) — THREE separate top-level fields
openAIMessage{
    Role:             "assistant",
    ReasoningContent: &"Let me think...",
    Content:          "The answer is 42",
    ToolCalls: []openAIToolCall{
        {ID:"call_abc", Function: {Name:"read_file", Arguments:"{\"path\":\"/tmp/foo\"}"}},
    },
}
```

### Why this matters: Adapter complexity

The Anthropic adapter is a **direct 1:1 mapping** — each `ContentPart` becomes one `anthropicContentBlock`:

```go
// Anthropic — simple type switch, one block per ContentPart
for _, part := range msg.Content {
    switch v := part.(type) {
    case llm.TextPart:
        → {Type:"text", Text: v.Text}
    case llm.ReasoningPart:
        → {Type:"thinking", Thinking: &v.Text}
    case llm.ToolUsePart:
        → {Type:"tool_use", ID: v.ID, Name: v.ToolName, Input: v.Input}
    case llm.ToolResultPart:
        → {Type:"tool_result", ToolUseID: v.ID, Content: ...}
    }
}
```

The OpenAI adapter must **split** a single `[]ContentPart` across three independent fields:

```go
// OpenAI — must distribute ContentParts into separate wire fields
apiMsg.Content = ...          // only TextParts go here
apiMsg.ReasoningContent = ... // only ReasoningParts go here
apiMsg.ToolCalls = ...        // only ToolUseParts go here
// ToolResultParts become entirely separate messages with role="tool"
```

And on receive, both providers use the same pattern: accumulate content by `index` across streaming chunks, then assemble into a single `[]ContentPart` at step completion. OpenAI accumulates three parallel fields (reasoning, text, tool arguments) by index; Anthropic accumulates content blocks by index — structurally the same approach.

**Conclusion:** The domain layer was clearly inspired by Anthropic's content block array model. It's the more general and extensible design — adding a new content type just means adding a new `ContentPart` implementation and a new case in each provider's switch statement. OpenAI's flat-field model is the odd one out requiring non-trivial split/merge logic.

## Wire Format Comparison

| Domain Type | OpenAI Wire | Anthropic Wire |
|---|---|---|
| `TextPart` | `content` (top-level field) | `content[]` array: `{type:"text", text:"..."}` |
| `ReasoningPart` | `reasoning_content` (top-level field) | `content[]` array: `{type:"thinking", thinking:"..."}` |
| `ToolUsePart` | `tool_calls[]` (top-level array) | `content[]` array: `{type:"tool_use", id, name, input}` |
| `ToolResultPart` | Separate message: `{role:"tool", tool_call_id, content}` (content is JSON-wrapped with `"status"` field — see note below) | `content[]` array: `{type:"tool_result", tool_use_id, content}`, **role remapped to "user"** |

> **Note on OpenAI tool result content format:** OpenAI's API has no native `is_error` field for tool results (unlike Anthropic). To prevent ambiguity — e.g., a tool returning `"no such file"` as an error vs. a file containing the literal text `"no such file"` — the OpenAI provider wraps tool results as JSON:
>
> - Success: `{"status":"success","data":"<plain text output>"}`
> - Error:   `{"status":"error","reason":"<error message>"}`
>
> This ensures the model can distinguish success from failure structurally rather than guessing from the content string. The Anthropic provider uses the native `is_error: true` flag instead, so results remain unwrapped plain text.

## Receiving (Wire → Domain)

### Example 1: Reasoning only

**OpenAI wire:**
```
Chunk 1: {"choices":[{"delta":{"reasoning_content":"Let me think..."}}]}
Chunk 2: {"choices":[{"delta":{"reasoning_content":" about this"}}]}
```

**Anthropic wire:**
```
event: content_block_start / {"type":"thinking","thinking":""}
event: content_block_delta / {"delta":{"type":"thinking_delta","thinking":"Let me think..."}}
event: content_block_delta / {"delta":{"type":"thinking_delta","thinking":" about this"}}
event: content_block_stop
```

**Domain output (same for both):**
```go
// Stream events:
ReasoningDeltaEvent{Delta: "Let me think..."}
ReasoningDeltaEvent{Delta: " about this"}

// Final message:
Message{
    Role: "assistant",
    Content: []ContentPart{
        ReasoningPart{
            Type: "reasoning",
            Text: "Let me think... about this",
        },
    },
}
```

### Example 2: Tool calls only

**OpenAI wire** (args arrive as chunks linked by `index`):
```
Chunk 1: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"id":"call_abc","function":{"name":"read_file"}}
]}}]}

Chunk 2: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"{\"path\":"}}
]}}]}

Chunk 3: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"\"/tmp/foo\""}}
]}}]}

Chunk 4: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"}"}}
]}}]}
```

**Anthropic wire** (tool call is a block lifecycle):
```
event: content_block_start / {"type":"tool_use","id":"toolu_abc","name":"read_file"}
event: content_block_delta / {"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}
event: content_block_delta / {"delta":{"type":"input_json_delta","partial_json":"\"/tmp/foo\""}}
event: content_block_delta / {"delta":{"type":"input_json_delta","partial_json":"}"}}
event: content_block_stop
```

**Domain output (same for both):**
```go
// Stream event at name arrival:
ToolUseStartEvent{ID: "call_abc", ToolName: "read_file"}

// Stream event at completion (after all args received):
ToolUsePart{
    ID: "call_abc",
    ToolName:   "read_file",
    Input:      json.RawMessage(`{"path":"/tmp/foo"}`),
}

// Final message:
Message{
    Role: "assistant",
    Content: []ContentPart{
        ToolUsePart{
            Type:       "tool_use",
            ID: "call_abc",
            ToolName:   "read_file",
            Input:      json.RawMessage(`{"path":"/tmp/foo"}`),
        },
    },
}
```

### Example 3: Reasoning + Tool calls (mixed in same message)

**OpenAI wire** (both fields arrive interleaved, accumulated separately):
```
Chunk 1: {"choices":[{"delta":{"reasoning_content":"Read file"}}]}
Chunk 2: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"id":"call_abc","function":{"name":"read_file"}}
]}}]}
Chunk 3: {"choices":[{"delta":{"reasoning_content":" to check"}}]}
Chunk 4: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"{\"path\":"}}
]}}]}
Chunk 5: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"\"/tmp/foo\""}}
]}}]}
Chunk 6: {"choices":[{"delta":{"tool_calls":[
  {"index":0,"function":{"arguments":"}"}}
]}}]}
```

**Domain output:**
```go
// Interleaved stream events:
ReasoningDeltaEvent{Delta: "Read file"}
ToolUseStartEvent{ID: "call_abc", ToolName: "read_file"}
ReasoningDeltaEvent{Delta: " to check"}
// (no more ReasoningDelta or ToolUseStart — just args accumulating)

ToolUsePart{
    ID: "call_abc",
    ToolName:   "read_file",
    Input:      json.RawMessage(`{"path":"/tmp/foo"}`),
}

// Final message — both in one Message.Content:
Message{
    Role: "assistant",
    Content: []ContentPart{
        ReasoningPart{Type: "reasoning", Text: "Read file to check"},
        ToolUsePart{
            Type: "tool_use", ID: "call_abc",
            ToolName: "read_file",
            Input:    json.RawMessage(`{"path":"/tmp/foo"}`),
        },
    },
}
```

**Why this works:** The `openAIStreamState` has a single `toolAccumulators[index]` per tool call, storing both metadata and argument fragments. Reasoning text accumulates independently in `reasoningBuilder`. They never interfere. `getMessage()` simply appends all non-empty accumulators to the Content slice.

## Sending (Domain → Wire)

### Example 4: Message with reasoning + tool calls

**Domain input:**
```go
Message{
    Role: "assistant",
    Content: []ContentPart{
        ReasoningPart{Type: "reasoning", Text: "Let me read the file"},
        ToolUsePart{
            ID: "call_abc",
            ToolName:   "read_file",
            Input:      json.RawMessage(`{"path":"/tmp/foo"}`),
        },
    },
}
```

**OpenAI wire output** (flat fields):
```json
{
    "role": "assistant",
    "reasoning_content": "Let me read the file",
    "tool_calls": [{
        "id": "call_abc",
        "type": "function",
        "function": {
            "name": "read_file",
            "arguments": "{\"path\":\"/tmp/foo\"}"
        }
    }]
}
```

**Anthropic wire output** (array of content blocks):
```json
{
    "role": "assistant",
    "content": [
        {"type": "thinking", "thinking": "Let me read the file"},
        {"type": "tool_use", "id": "call_abc", "name": "read_file",
         "input": {"path": "/tmp/foo"}}
    ]
}
```

### Wire Format Differences (Anthropic vs OpenAI)

| Aspect | OpenAI | Anthropic |
|---|---|---|
| **Message structure** | Flat fields (`content`, `reasoning_content`, `tool_calls` at top level) | Content is always `[]anthropicContentBlock` array |
| **Tool result role** | `"tool"` | Remapped to `"user"` |
| **Tool call args encoding** | Double-encoded JSON string (`json.Marshal(string(rawMsg))`) | Raw JSON object (`json.RawMessage` directly) |
| **Empty reasoning when reasoning mode is on** | Sets `"reasoning_content": ""` (string pointer) | Prepends `{"type":"thinking","thinking":""}` to content array |
| **SSE event format** | Data-only lines, `[DONE]` terminator | Named events (`message_start`, `content_block_start`, etc.) |
| **Tool call arg chunks** | Linked by `index` field across multiple deltas | Grouped by block lifecycle (start → delta → stop) |

## Stream State Machines

### OpenAI: Parallel accumulators

```
openAIStreamState {
    textBuilder       strings.Builder                ← "content" delta chunks
    reasoningBuilder  strings.Builder                ← "reasoning_content" delta chunks
    toolAccumulators  map[int]*openAIToolAccumulator ← tool calls keyed by index
}

openAIToolAccumulator {
    id   string          ← tool call id
    name string          ← function name
    args strings.Builder ← accumulated arguments fragments
}
```

All three accumulate simultaneously during streaming. At `StepCompleteEvent`, they merge into a single `Message.Content` slice.

### Anthropic: Indexed block accumulator (like OpenAI)

```
blockAccumulator {
    blockType string              // "text" | "thinking" | "tool_use"
    buffer    strings.Builder     // text, thinking, or tool_use partial_json
    id, name string
}

anthropicStreamState {
    contentParts  map[int]ContentPart          ← finished blocks by index
    blocks        map[int]*blockAccumulator    ← in-progress blocks by index
}
```

Every wire event carries an `index` (start, delta, stop), just like OpenAI's `tool_calls[index]`. Blocks may arrive interleaved — block 1 can start before block 0 finishes. Each block is independently accumulated by index. `content_block_stop(i)` stores the result in `contentParts[i]`, and `getMessage()` sorts by index to produce the final ordered slice.
