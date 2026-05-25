# Step Messages

Each agentic step is one LLM API round trip. This doc explains how messages are organized within a step.

## How many messages per step?

A step can have 1 or 2 messages:

| Messages | When |
|----------|------|
| `[assistantMsg]` | Model responds with text only (no tool calls) |
| `[assistantMsg, toolResultMsg]` | Model responds with one or more tool calls |

### Text only (no tools)

```go
// agent.go:123
if callbacks.OnStepFinish != nil {
    callbacks.OnStepFinish(stepMessages, stepUsage)
}
```

`stepMessages` comes straight from the provider's `StepCompleteEvent.Messages`:

```go
// anthropic.go:464, openai.go:297
yield(llm.StepCompleteEvent{
    Messages:   []llm.Message{state.getMessage()},  // [assistantMsg]
    ...
}, nil)
```

**Result:** `OnStepFinish` gets `[assistantMsg]`.

### With tool calls

When the model returns tool calls, the agent loop adds a tool result message:

```go
// agent.go:168-169
toolResults := a.executeTools(ctx, toolCalls, callbacks)
toolResultMsg := Message{Role: RoleTool, Content: toolResults}
```

`executeTools` runs tool calls one by one (sequentially) and collects all results into a single `[]ContentPart`. This becomes the content of one tool result message.

```go
// agent.go:187-192
stepWithResults := make([]Message, len(stepMessages), len(stepMessages)+1)
copy(stepWithResults, stepMessages)                  // [assistantMsg]
stepWithResults = append(stepWithResults, toolResultMsg)  // [assistantMsg, toolResultMsg]
callbacks.OnStepFinish(stepWithResults, stepUsage)
```

**Result:** `OnStepFinish` gets `[assistantMsg, toolResultMsg]`.

Even with multiple tool calls, the structure is:

```
Step: [assistantMsg(Content: [text, tool_use1, tool_use2, tool_use3...]),
       toolResultMsg(Content: [tool_result1, tool_result2, tool_result3...])]
```

All tool results go into **one** tool result message, not one per tool call.

## Flow summary

```
Provider streams response
       │
       ▼
StepCompleteEvent{Messages: [assistantMsg]}
       │
       ▼
processStreamEvents() → stepMessages, toolCalls
       │
       ├── no tool calls ──► OnStepFinish([assistantMsg])
       │
       └── tool calls ──► executeTools() → toolResultMsg
                          OnStepFinish([assistantMsg, toolResultMsg])
```

## Important rules

- **Use `StepCompleteEvent.Messages` as-is.** Don't reconstruct the assistant message from tool calls — that loses text and reasoning the model returned alongside them.
- **The agent loop extends the slice, it doesn't replace it.** It takes `[assistantMsg]` and appends the tool result message.
- **Why one tool result message?** Both Anthropic and OpenAI require all tool results for a step in a single `role: "tool"` message. Multiple tool messages would break the API schema.
