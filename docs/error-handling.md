# Error Handling

How AlayaCore detects and propagates errors from LLM API providers during streaming responses.

## Overview

Both OpenAI and Anthropic APIs use finish reasons (or stop reasons) to indicate why a streaming response ended. AlayaCore monitors these reasons to detect errors and prevent silent failures — cases where incomplete responses could be processed as if complete, the agent loop stops without explanation, or users see "context: 0" with no error message.

## OpenAI Provider

### Valid Finish Reasons

| Finish Reason | Meaning | Handling |
|---------------|---------|----------|
| `stop` | Normal completion | Process the full message |
| `length` | Hit `max_tokens` limit, response is truncated | **Error** — `ErrResponseTruncated` |
| `tool_calls` | Model wants to call tools | Execute tools and continue |
| `""` (empty) | Still streaming | Continue processing |

### Error Finish Reasons

| Finish Reason | Meaning | Handling |
|---------------|---------|----------|
| `content_filter` | Content blocked by safety filters | **Error** — `"content blocked by safety filter"` |
| Any other value | Unknown reason | **Error** — `"stream finished with unexpected reason: ..."` |

### Implementation

`internal/llm/providers/openai.go` → `handleEvent`:

```go
if choice.FinishReason == "content_filter" {
	return fmt.Errorf("content blocked by safety filter")
}
if choice.FinishReason != "" && choice.FinishReason != "stop" &&
	choice.FinishReason != "length" && choice.FinishReason != "tool_calls" {
	return fmt.Errorf("stream finished with unexpected reason: %s", choice.FinishReason)
}
```

## Anthropic Provider

### Valid Stop Reasons

| Stop Reason | Meaning | Handling |
|-------------|---------|----------|
| `end_turn` | Natural stopping point | Normal completion |
| `max_tokens` | Hit token limit | **Error** — `ErrResponseTruncated` |
| `stop_sequence` | Hit custom stop sequence | Valid |
| `tool_use` | Tool call complete | Execute tool and continue |
| `pause_turn` | Server-side extended turn | Valid — part of extended flow |

### Error Stop Reasons

| Stop Reason | Meaning | Handling |
|-------------|---------|----------|
| `refusal` | Model refused (content policy) | **Error** — `"model refused to respond: content policy violation"` |
| Any other value | Unknown reason | **Error** — `"stream finished with unexpected stop reason: ..."` |

### Implementation

`internal/llm/providers/anthropic.go` → `handleMessageDelta`:

```go
if stopReason == "refusal" {
	return fmt.Errorf("model refused to respond: content policy violation")
}
if stopReason != "" && stopReason != "end_turn" && stopReason != "max_tokens" &&
	stopReason != "stop_sequence" && stopReason != "tool_use" && stopReason != "pause_turn" {
	return fmt.Errorf("stream finished with unexpected stop reason: %s", stopReason)
}
```

## Error Propagation

### Provider-level errors (content filter, refusal, unknown reasons)

1. The provider returns an error from the event handler via the streaming iterator's error parameter (`yield(nil, err)`)
2. The agent's event loop in `streamEvents` receives the error from the `iter.Seq2[StreamEvent, error]` iterator (`for event, err := range events`)
3. The agent loop terminates and returns the error to the caller
5. The UI displays the error message to the user

### Agent-level errors (truncation, max steps)

Truncation (`max_tokens` / `length`) is **not** an error at the provider level — the response is valid, just incomplete. The provider includes the stop reason in `StepCompleteEvent.StopReason`.

The agent detects truncation in `streamEvents` and checks for it in `Stream()`. Partial messages are still included in the `StreamResult` so the caller can inspect what was generated before the cutoff.

```go
// In streamEvents:
case StepCompleteEvent:
    stepMessage = e.Message
    stepUsage = e.Usage
    if e.StopReason == "max_tokens" || e.StopReason == "length" {
        truncated = true
    }
```

## Error Recovery

When an error occurs during prompt processing, the prompt **fails** instead of continuing.

Errors include:
- **Provider errors** — network failure, API error, content filter, refusal
- **Max steps exceeded** — agent loop hit `--max-steps` limit without final response (error: `"agent loop exceeded maximum steps"`)
- **Response truncated** — model hit output token limit (error: `"response truncated: hit output token limit"`)

### Recovery

The user can:
- `:continue` — retry the last prompt
- `:model_set` — switch to a different model, then `:continue`
- Type a new prompt — the next prompt is sent as a new user message

### `:continue`

`:continue` runs in a separate goroutine — it can be canceled with `:cancel` while the LLM call is in progress. It resends the last prompt to the LLM. See [commands.md](commands.md) for details.

No queue management needed.

## Testing

Error handling is tested in `internal/llm/providers/providers_test.go`:

| Test | Verifies |
|------|----------|
| `TestOpenAINetworkError` | Unexpected finish reasons trigger error |
| `TestOpenAIContentFilter` | `content_filter` triggers error |
| `TestOpenAILengthFinishReason` | `length` doesn't trigger provider error, `StopReason` is populated |
| `TestAnthropicRefusalStopReason` | `refusal` triggers error |
| `TestAnthropicUnknownStopReason` | Unknown stop reasons trigger error |
| `TestAnthropicValidStopReasons` | All valid reasons don't trigger errors, `StopReason` is populated |

Max steps and truncation behavior is tested in `internal/llm/agent_maxsteps_test.go`:

| Test | Verifies |
|------|----------|
| `TestAgentMaxStepsExceeded` | Agent returns `ErrMaxStepsExceeded` after reaching limit, with accumulated messages |
| `TestAgentCompletesWithinMaxSteps` | Agent completes normally when final response arrives before limit |
| `TestAgentTruncatedMaxTokens` | Agent returns `ErrResponseTruncated` for `max_tokens`, partial messages preserved |
| `TestAgentTruncatedLength` | Agent returns `ErrResponseTruncated` for `length`, partial messages preserved |
| `TestAgentNoTruncationOnEndTurn` | Agent does not return `ErrResponseTruncated` for `end_turn` |
