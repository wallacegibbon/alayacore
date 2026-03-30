# Context Token Tracking

## Overview

`ContextTokens` in `Session` tracks the current conversation's context size (input tokens) as reported by the LLM provider. It is used for:

- Displaying context usage to the user (e.g. `context: 2118 / 128000`)
- Triggering auto-summarization when context exceeds 80% of `ContextLimit`

## How It Works

### Data Flow

```
Provider API response
  → Provider extracts usage (InputTokens, CacheReadTokens, CacheCreationTokens)
    → StepCompleteEvent carries Usage
      → Agent.Stream calls OnStepFinish callback with stepUsage
        → Session.trackUsage(stepUsage)
          → ContextTokens = InputTokens + CacheReadTokens + CacheCreationTokens
```

### The `trackUsage` Function

```go
func (s *Session) trackUsage(usage llm.Usage) {
    s.mu.Lock()
    s.TotalSpent.InputTokens += usage.InputTokens
    s.TotalSpent.OutputTokens += usage.OutputTokens
    s.ContextTokens = usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
    s.mu.Unlock()
    s.sendSystemInfo()
}
```

Key design decisions:

- **Overwrite (`=`), not accumulate (`+=`).** Each API call's `InputTokens` already represents the *entire conversation history* sent in that request. Accumulating would double-count.
- **Only the last step's value matters.** For multi-step tool call loops, each step re-sends the full history (plus new messages). The last step has the most complete count.
- **Cache tokens are additive.** Anthropic reports `InputTokens` as the non-cached portion; `CacheReadTokens` and `CacheCreationTokens` are separate. The sum gives the true context size.

### Multi-Step Tool Calls

When the agent loop runs multiple steps (tool call → tool result → next step), `trackUsage` is called once per step via `OnStepFinish`. Each call overwrites `ContextTokens` with that step's full-context measurement:

```
Step 1 (tool call):     InputTokens=500, CacheRead=8000 → ContextTokens = 8500
Step 2 (tool response): InputTokens=900, CacheRead=8000 → ContextTokens = 8900  ← final, correct value
```

The last step's value is always the most accurate because it includes all prior tool results.

## Provider Differences

### Anthropic Protocol

Reports usage across multiple SSE events (`message_start`, `message_delta`, `message_stop`). The provider's `extractAndSetUsage` merges partial chunks:

| Event | Fields Present |
|---|---|
| `message_start` | `input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens` |
| `message_delta` | `output_tokens` |
| `message_stop` | (sometimes final usage) |

`InputTokens` = non-cached portion only. Cache tokens are separate.

### OpenAI Protocol

Reports usage in a single chunk with `prompt_tokens` and `completion_tokens`:

| Field | Meaning |
|---|---|
| `prompt_tokens` | Full context size (all messages sent) |
| `completion_tokens` | Output tokens generated |

`prompt_tokens` already includes any cached tokens — no separate cache fields.

## Model Switching and Token Count Changes

When switching models (e.g. Anthropic → OpenAI), the reported context size may change even though the conversation history is unchanged. This is expected and not a bug — **different providers use different tokenizers**.

### Example from Debug Logs

Conversation: "my cpu type" with tool call, then model switch, then "my os type" with tool call.

| Step | Provider | ContextTokens | API Reported |
|---|---|---|---|
| Prompt 1, Step 1 (tool call) | Anthropic/llama.cpp | 1149 | input=4, cache_read=1145 |
| Prompt 1, Step 2 (answer) | Anthropic/llama.cpp | 2118 | input=973, cache_read=1145 |
| Model switch | → OpenAI/glm-5.1 | (unchanged) | — |
| Prompt 2, Step 1 (tool call) | OpenAI/glm-5.1 | 2073 | prompt_tokens=2073 |
| Prompt 2, Step 2 (answer) | OpenAI/glm-5.1 | 2317 | prompt_tokens=2317 |

The apparent "drop" from 2118 to 2073 after model switch is simply the difference in tokenization between the two models. The full conversation was sent correctly; only the counting method changed.

## Related

- `shouldAutoSummarize()` — triggers when `ContextTokens >= ContextLimit * 80%`
- `summarize()` — replaces conversation history with a summary, resets `ContextTokens` to the summary's output token count
- `applyModelContextLimit()` — sets `ContextLimit` from the active model's config
