# Step Messages

A **step** is one LLM round trip. It produces 1 or 2 messages in the conversation history:

- `[assistantMsg]` — text-only response (no tool calls)
- `[assistantMsg, toolResultMsg]` — tool calls followed by tool results

## Flow

1. `Stream()` calls the provider and processes streaming events via `streamEvents()`
2. `streamEvents()` handles both no-confirm tools (execute immediately in goroutines) and deferred tools (confirm then execute), collecting all results into one slice. It then re-orders tool results by ID to match the assistant message's content order and returns 1–2 pre-assembled messages (`[assistantMsg]` or `[assistantMsg, toolResultMsg]`).
3. `Stream()` appends the returned messages to `allMessages`, fires `OnStepFinish(allMessages, stepUsage)`, and checks whether the task is done.
4. The session receives the *full* history and replaces its own copy. `Stream()` also returns the final messages as a convenience.
5. Loop repeats until the model responds with text only (no tool calls) or the response is truncated.

## Key details

- **`StepCompleteEvent.Message`** is a single `Message` (the assistant message). Tool calls, text, and reasoning are all content parts within it.
- **Tool execution** starts during streaming (no-confirm tools execute immediately via goroutines) and continues after streaming (deferred tools are confirmed then executed). All results flow through a shared channel and are collected in a single loop. Results are matched to their tool call by ID.
- **All tool results** go into one tool result message per step (required by both Anthropic and OpenAI).
- **Incomplete tool calls on cancel:** When user cancels mid-tool-call, messages may have `tool_use` without matching `tool_result`. `cleanIncompleteToolInputs()` removes these to prevent API errors on next request.
- **Tool result message ordering:** `OnStepFinish` receives complete step messages including both the assistant message (with tool calls) and the tool result message. `OnToolOutput` should only send UI notifications — the agent loop handles message assembly.
