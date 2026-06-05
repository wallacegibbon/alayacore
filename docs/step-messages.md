# Step Messages

A **step** is one LLM round trip. It produces 1 or 2 messages in the conversation history:

- `[assistantMsg]` — text-only response (no tool calls)
- `[assistantMsg, toolResultMsg]` — tool calls followed by tool results

## Flow

1. `Stream()` calls the provider and processes streaming events via `processStreamEvents()`
2. That returns a single `Message` (the assistant message with all content parts) and token usage
3. `executeStep()` appends the assistant message to `allMessages`, extracts tool calls from its content and executes them via `executeTools()`, appends their results as one tool result message, then fires `OnStepFinish(allMessages, stepUsage)`
4. The session receives the *full* history and replaces its own copy. `Stream()` also returns the final messages as a convenience.
5. Loop repeats until the model responds with text only (no tool calls) or the response is truncated.

## Key details

- **`StepCompleteEvent.Message`** is a single `Message` (the assistant message). Tool calls, text, and reasoning are all content parts within it.
- **Tool execution** is driven by extracting `ToolUsePart`s from `stepMessage.Content` in `executeStep`, not from a separate collection.
- **All tool results** go into one tool result message per step (required by both Anthropic and OpenAI).
- **Incomplete tool calls on cancel:** When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolUses()` removes these to prevent API errors on next request.
- **Tool result message ordering:** `OnStepFinish` receives complete step messages including both the assistant message (with tool calls) and the tool result message. `OnToolUseOutput` should only send UI notifications — the agent loop handles message assembly.
