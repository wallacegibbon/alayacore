# Provider Wire Format → Domain Type Mapping

How OpenAI and Anthropic API wire formats map to the domain types in `llm/types.go`.

## Domain Layer Overview

All providers eat different wire formats and emit the same domain types:

```go
// llm/types.go — the domain types

type ContentPart interface { isContentPart() }

// Implementations:
type TextPart      struct { Type, Text string }
type ReasoningPart struct { Type, Text, Signature string }
type ToolCallPart  struct { Type, ToolCallID, ToolName string; Input json.RawMessage }
type ToolResultPart struct { Type, ToolCallID string; Output ToolResultOutput }

type Message struct {
    Role    MessageRole   // "system" | "user" | "assistant" | "tool"
    Content []ContentPart
}

type StreamEvent interface { isStreamEvent() }

// Implementations:
type TextDeltaEvent      struct { Delta string }
type ReasoningDeltaEvent struct { Delta string }
type ToolCallStartEvent  struct { ToolCallID, ToolName string }
type ToolCallEvent       struct { ToolCallID, ToolName string; Input json.RawMessage }
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
        ToolCallPart{Type:"tool_use", ToolCallID:"call_abc", ToolName:"read_file",
                     Input: json.RawMessage(`{"path":"/tmp/foo"}`)},
    },
}
```

```go
// Anthropic wire (anthropic.go) — array of concrete blocks, nearly 1:1
anthropicMessage{
    Role: "assistant",
    Content: []anthropicContentBlock{
        {Type:"thinking", Thinking: &"Let me think...", Signature: "abc123"},
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
        → {Type:"thinking", Thinking: &v.Text, Signature: v.Signature}
    case llm.ToolCallPart:
        → {Type:"tool_use", ID: v.ToolCallID, Name: v.ToolName, Input: v.Input}
    case llm.ToolResultPart:
        → {Type:"tool_result", ToolUseID: v.ToolCallID, Content: ...}
    }
}
```

The OpenAI adapter must **split** a single `[]ContentPart` across three independent fields:

```go
// OpenAI — must distribute ContentParts into separate wire fields
apiMsg.Content = ...          // only TextParts go here
apiMsg.ReasoningContent = ... // only ReasoningParts go here
apiMsg.ToolCalls = ...        // only ToolCallParts go here
// ToolResultParts become entirely separate messages with role="tool"
```

And on receive, the OpenAI adapter must **merge** three independent stream accumulators (`reasoningBuilder`, `textBuilder`, `toolCallArgs[]`) back into a single `[]ContentPart` — while Anthropic's blocks arrive already serialized and just need direct field mapping.

**Conclusion:** The domain layer was clearly inspired by Anthropic's content block array model. It's the more general and extensible design — adding a new content type just means adding a new `ContentPart` implementation and a new case in each provider's switch statement. OpenAI's flat-field model is the odd one out requiring non-trivial split/merge logic.

## Wire Format Comparison

| Domain Type | OpenAI Wire | Anthropic Wire |
|---|---|---|
| `TextPart` | `content` (top-level field) | `content[]` array: `{type:"text", text:"..."}` |
| `ReasoningPart` | `reasoning_content` (top-level field) | `content[]` array: `{type:"thinking", thinking:"...", signature:"..."}` |
| `ToolCallPart` | `tool_calls[]` (top-level array) | `content[]` array: `{type:"tool_use", id, name, input}` |
| `ToolResultPart` | Separate message: `{role:"tool", tool_call_id, content}` | `content[]` array: `{type:"tool_result", tool_use_id, content}`, **role remapped to "user"** |

## Receiving (Wire → Domain)

### Example 1: Reasoning only

**OpenAI wire:**
```
Chunk 1: {"choices":[{"delta":{"reasoning_content":"Let me think..."}}]}
Chunk 2: {"choices":[{"delta":{"reasoning_content":" about this"}}]}
```

**Anthropic wire:**
```
event: content_block_start / {"type":"thinking","signature":"abc123"}
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
            // Signature: "abc123"  ← anthropic only, empty for OpenAI
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
ToolCallStartEvent{ToolCallID: "call_abc", ToolName: "read_file"}

// Stream event at completion (after all args received):
ToolCallEvent{
    ToolCallID: "call_abc",
    ToolName:   "read_file",
    Input:      json.RawMessage(`{"path":"/tmp/foo"}`),
}

// Final message:
Message{
    Role: "assistant",
    Content: []ContentPart{
        ToolCallPart{
            Type:       "tool_use",
            ToolCallID: "call_abc",
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
ToolCallStartEvent{ToolCallID: "call_abc", ToolName: "read_file"}
ReasoningDeltaEvent{Delta: " to check"}
// (no more ReasoningDelta or ToolCallStart — just args accumulating)

ToolCallEvent{
    ToolCallID: "call_abc",
    ToolName:   "read_file",
    Input:      json.RawMessage(`{"path":"/tmp/foo"}`),
}

// Final message — both in one Message.Content:
Message{
    Role: "assistant",
    Content: []ContentPart{
        ReasoningPart{Type: "reasoning", Text: "Read file to check"},
        ToolCallPart{
            Type: "tool_use", ToolCallID: "call_abc",
            ToolName: "read_file",
            Input:    json.RawMessage(`{"path":"/tmp/foo"}`),
        },
    },
}
```

**Why this works:** The `openAIStreamState` has **two independent accumulators** — `reasoningBuilder` for reasoning text, and `toolCallArgs[index]` for each tool's arguments. They never interfere. `getMessage()` simply appends all non-empty accumulators to the Content slice.

## Sending (Domain → Wire)

### Example 4: Message with reasoning + tool calls

**Domain input:**
```go
Message{
    Role: "assistant",
    Content: []ContentPart{
        ReasoningPart{Type: "reasoning", Text: "Let me read the file"},
        ToolCallPart{
            ToolCallID: "call_abc",
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
        {"type": "thinking", "thinking": "Let me read the file", "signature": ""},
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
| **Thinking signature** | Not used | Required, passed back verbatim from API |
| **Empty reasoning when reasoning mode is on** | Sets `"reasoning_content": ""` (string pointer) | Prepends `{"type":"thinking","thinking":""}` to content array |
| **SSE event format** | Data-only lines, `[DONE]` terminator | Named events (`message_start`, `content_block_start`, etc.) |
| **Tool call arg chunks** | Linked by `index` field across multiple deltas | Grouped by block lifecycle (start → delta → stop) |

## Stream State Machines

### OpenAI: Parallel accumulators

```
openAIStreamState {
    textBuilder      strings.Builder       ← "content" delta chunks
    reasoningBuilder strings.Builder       ← "reasoning_content" delta chunks
    toolCallArgs     map[int]*Builder      ← "tool_calls[*].function.arguments" by index
    toolCalls        []ToolCallPart        ← tool call metadata by index
}
```

All four accumulate simultaneously during streaming. At `StepCompleteEvent`, they merge into a single `Message.Content` slice.

### Anthropic: Serial block processor

```
anthropicStreamState {
    contentParts  []ContentPart            ← finished blocks appended here

    // Current block being built:
    currentType   string                   // "text" | "thinking" | "tool_use"
    currentText   strings.Builder
    currentInput  strings.Builder          // tool_use partial_json
    currentID, currentName, currentSignature string
}
```

Blocks arrive serially (one `content_block_start` → deltas → `content_block_stop` at a time). Each block finishes before the next starts. `finishBlock()` converts the current block to a `ContentPart` and appends to `contentParts`.
