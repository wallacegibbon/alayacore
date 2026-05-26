# Step Messages

A **step** is one LLM round trip. It produces 1 or 2 messages in the conversation history:

- `[assistantMsg]` — text-only response (no tool calls)
- `[assistantMsg, toolResultMsg]` — tool calls followed by tool results

## Flow

1. `Stream()` calls the provider and processes streaming events via `processStreamEvents()`
2. That returns `stepMessages` (1-element `[]Message` from `StepCompleteEvent`, for history) and `toolCalls` (flat `[]ToolCallPart`, for execution)
3. `finalizeStep()` appends the assistant message to `allMessages`, runs tools (if any), appends their results as one tool result message, then fires `OnStepFinish(allMessages, stepUsage)`
4. The session receives the *full* history and replaces its own copy. `Stream()` also returns the final messages as a convenience.
5. Loop repeats until the model responds with text only (no tool calls) or the response is truncated.

## Key details

- **`StepCompleteEvent.Messages`** is `[]Message` with exactly 1 element (the assistant message). It's a slice so `append(allMessages, stepMessages...)` works directly.
- **Don't reconstruct** the assistant message from tool calls — that loses text/reasoning the model returned alongside them. (The code does reconstruct as a fallback if `stepMessages` is empty, but normally it shouldn't be.)
- **All tool results** go into one tool result message per step (required by both Anthropic and OpenAI).
- **Incomplete tool calls on cancel:** When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolCalls()` removes these to prevent API errors on next request.
- **Tool result message ordering:** `OnStepFinish` receives complete step messages including both the assistant message (with tool calls) and the tool result message. `OnToolResult` should only send UI notifications — the agent loop handles message assembly.
