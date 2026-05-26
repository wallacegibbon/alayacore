# Step Messages

Each agentic step is one LLM API round trip. This doc explains how messages are organized within a step and how data flows from the provider stream through the agent.

## How many messages per step?

A step can have 1 or 2 messages:

| Messages | When |
|----------|------|
| `[assistantMsg]` | Model responds with text only (no tool calls) |
| `[assistantMsg, toolResultMsg]` | Model responds with one or more tool calls |

### Text only (no tools)

`stepMessages` comes straight from the provider's `StepCompleteEvent.Messages`:

```go
// anthropic.go:464, openai.go:297
yield(llm.StepCompleteEvent{
    Messages:   []llm.Message{state.getMessage()},  // [assistantMsg]
    ...
}, nil)
```

The agent appends it to `allMessages` and fires `OnStepFinish` with the full history:

```go
// agent.go:142-146
allMessages = append(allMessages, stepMessages...)
OnStepFinish(allMessages, stepUsage)
```

**Result:** `OnStepFinish` receives `allMessages` which is `[..., assistantMsg]`.

### With tool calls

When the model returns tool calls, the agent loop runs the tools and appends results:

```go
// agent.go:198-199
allMessages = append(allMessages, stepMessages...)  // [assistantMsg]
allMessages = append(allMessages, toolResultMsg)    // [toolResultMsg]
OnStepFinish(allMessages, stepUsage)                 // full history
```

`executeTools` runs tool calls one by one (sequentially) and collects all results into a single `[]ContentPart`. This becomes the content of one tool result message.

**Result:** `OnStepFinish` receives `allMessages` which is `[..., assistantMsg, toolResultMsg]`.

Even with multiple tool calls, the structure is:

```
Step contribution: [assistantMsg(Content: [text, tool_use1, tool_use2, ...]),
                    toolResultMsg(Content: [tool_result1, tool_result2, ...])]
```

All tool results go into **one** tool result message, not one per tool call.

## Provider-level streaming data flow

This is the detailed view of how the provider emits events and how `processStreamEvents` (agent.go) assembles them:

```
Provider streams response (SSE events)
  │
  ├── TextDeltaEvent      ──► OnTextDelta (UI streaming)
  │
  ├── ReasoningDeltaEvent ──► OnReasoningDelta (UI streaming)
  │
  ├── ToolCallStartEvent  ──► OnToolCallStart (UI placeholder)
  │
  ├── ToolCallEvent × N   ──► collected into toolCalls slice ──► executeTools()
  │
  └── StepCompleteEvent
       ├── Messages: [assistantMsg]
       │    └── Content: [TextPart, ReasoningPart, ToolCallPart × N]
       │         ▲
       │         │    These ToolCallParts are for conversation history
       │         │    (preserving what the model said). They are NOT
       │         │    used to execute tools — the separate ToolCallEvent
       │         │    stream above is used for execution.
       │
       ├── Usage: token counts for this step
       └── StopReason: "end_turn" | "stop" | "max_tokens" | "length"
```

### Why two representations of tool calls?

This is a key design point. Tool calls appear in **two** return values from `processStreamEvents`:

| Return value | Source | Content | Purpose |
|---|---|---|---|
| `stepMessages` | `StepCompleteEvent.Messages` | `[assistantMsg]` with `ToolCallPart` in `.Content` | Appended to conversation history (`allMessages`) so the next API call has complete context |
| `toolCalls` | Accumulated `ToolCallEvent` events | Flat `[]ToolCallPart` | Used by `executeToolStep` to actually run each tool |

Reason: These two uses have different shapes and lifecycle needs.

- `stepMessages` preserves the exact assistant message structure (text + reasoning + tool calls together) so the LLM schema is maintained across rounds.
- `toolCalls` is a flat list that the agent iterates over to execute tools and collect results.

### Full agent loop data flow

```
Stream() loop (agent.go:91)
  │
  ├── OnStepStart(step)
  │
  ├── Provider.StreamMessages(ctx, allMessages, ...)
  │     │
  │     ▼
  ├── processStreamEvents()
  │     │
  │     ├── returns: stepMessages, stepUsage, toolCalls
  │     │
  │     ├── if truncErr != nil || len(toolCalls) == 0:
  │     │     allMessages += stepMessages         ← append first
  │     │     OnStepFinish(allMessages, stepUsage) ← then callback with full history
  │     │     break
  │     │
  │     └── if toolCalls > 0:
  │           allMessages += stepMessages
  │           allMessages += toolResultMsg
  │           OnStepFinish(allMessages, stepUsage) ← full history
  │           continue loop
  │
  └── return StreamResult{Messages: allMessages, Usage: totalUsage}
```

## How providers build the assistant message

Each provider accumulates streaming deltas into a single `Message{Role: RoleAssistant}`:

**Anthropic** (`anthropic.go`):
```
content_block_start ──► startBlock() — records block type, ID, name
content_block_delta ──► appendText() / appendInput() — accumulates text/JSON
content_block_stop  ──► finishBlock() → appends TextPart/ReasoningPart/ToolCallPart to contentParts
                        Also yields ToolCallEvent
message_stop        ──► getMessage() → Message{Content: contentParts}
                        Yields StepCompleteEvent{Messages: [getMessage()]}
```

**OpenAI** (`openai.go`):
```
delta.role="assistant" ──► accumulates textBuilder, reasoningBuilder, toolCalls
stream ends          ──► finalizeToolCalls()
                        Yields ToolCallEvent for each completed tool call
                        getMessage() → Message{Content: [reasoning?, text?, toolCalls...]}
                        Yields StepCompleteEvent{Messages: [getMessage()]}
```

## Session integration

The session layer owns the authoritative copy of the conversation history.
When a task runs, it passes `s.Messages` to `agent.Stream()` as the initial history.
The agent accumulates all steps into `allMessages` internally, and:

1. **During each step**: `OnStepFinish(allMessages, stepUsage)` notifies the session
   with the full updated history. Session replaces `s.Messages` directly:

   ```go
   // session_task.go
   OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
       s.Messages = messages  // allMessages from agent — full history
       s.sendEvent(eventStepFinish{messages, ...})
   }
   ```

2. **After all steps**: `Stream()` returns `StreamResult{Messages: allMessages}`.
   The session already has the data via OnStepFinish, so the return value
   is available but not required.

3. **Between tasks**: `s.Messages` (owned by the `run()` goroutine) is the
   single source of truth. On task completion, `taskResult` channel returns
   the final messages and `handleTaskDone` replaces `s.Messages`.

```
run() goroutine (s.Messages)
  │
  ├── tryStartNextTask()
  │     taskMessages = copy(s.Messages)
  │     go runTask(item, taskMessages)
  │
  │     task goroutine (taskMessages — local copy)
  │     └── handleUserPrompt / runTaskCommand
  │           └── processPrompt(ctx, taskMessages)
  │                 └── agent.Stream(ctx, taskMessages, callbacks)
  │                       │
  │                       │ allMessages = copy(taskMessages)
  │                       │ for each step:
  │                       │   allMessages += [step results]
  │                       │   OnStepFinish(allMessages, usage)
  │                       │     │
  │                       │     └── capture processResult = allMessages
  │                       │
  │                       └── return (processResult, outputTokens, err)
  │
  └── handleTaskDone(result)
        s.Messages = result  ← single handoff point
        saveSessionToFile(s.Messages)
```

## StreamResult — the final summary

`Stream()` returns `StreamResult{Messages: allMessages, Usage: totalUsage}`.

`Messages` is the full conversation history (same as what `OnStepFinish` receives).
`Usage` is the total token usage summed across all steps (whereas `OnStepFinish`'s
`usage` parameter is per-step).

```go
OnStepFinish step 1:  messages=allMessages, usage={in:100, out:50}
OnStepFinish step 2:  messages=allMessages, usage={in:200, out:80}

StreamResult:          Messages=allMessages, Usage={in:300, out:130}  // total sum

### When to use StreamResult vs OnStepFinish

| Scenario | Use |
|----------|-----|
| Real-time streaming UI (text deltas, tool calls, etc.) | `StreamCallbacks` + `OnStepFinish` |
| Only need final result, don't care about intermediate state | `StreamResult` (pass empty `StreamCallbacks{}`) |
| Test code verification | `StreamResult` — one call, all data |
| Session layer (needs live `s.Messages` updates) | `OnStepFinish` callback |

`StreamResult` exists as a convenience for callers that don't need
callbacks — one call gets all results without manual collection.

## Important rules

- **Use `StepCompleteEvent.Messages` as-is.** Don't reconstruct the assistant message from tool calls — that loses text and reasoning the model returned alongside them.
- **`OnStepFinish` receives the full `allMessages`, not just the current step.** The session replaces its state rather than appending increments.
- **Why one tool result message?** Both Anthropic and OpenAI require all tool results for a step in a single `role: "tool"` message. Multiple tool messages would break the API schema.
- **Don't use `stepMessages` for tool execution.** The tool calls embedded in `stepMessages[0].Content` are for history preservation only. Use the separate `toolCalls` return value for execution.

## Why `[]Message` not `Message`?

`StepCompleteEvent.Messages` is typed `[]Message` (a slice) even though it always contains exactly **1** element — the assistant message from the provider.

Why not `Message` directly? Because the same `[]Message` type flows through the entire pipeline:

```
Provider          → StepCompleteEvent.Messages:   [assistantMsg]
processStreamEvents returns:                       [assistantMsg]
allMessages accumulates:                           [history..., assistantMsg, toolResultMsg]
OnStepFinish receives:                             [history..., assistantMsg, toolResultMsg]
```

If `Messages` were `Message`, the agent would need to create a new `[]Message` from scratch when extending the conversation. With `[]Message`, `append` works directly.
