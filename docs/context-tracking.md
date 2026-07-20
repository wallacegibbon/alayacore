# Context Token Tracking

How AlayaCore tracks conversation context size across LLM API calls and providers.

## Overview

`ContextTokens` in `Session` tracks the current conversation's total context size (input + output + cache) as reported by the LLM provider. It is used for:

- Displaying context usage in the status bar (e.g. `2.1K/128K 1.7%`)
- Triggering auto-summarization when context exceeds 65% of `context_limit`

## Data Flow

```
Provider API response
  → Provider extracts usage (InputTokens, OutputTokens, CacheReadTokens, CacheCreationTokens)
    → Provider emits StreamEvent{Usage: ...}
      → Agent.streamEvents merges partial usage into stepUsage
        → Agent fires OnStepFinish(messages, stepUsage) callback
          → Session.sendEvent(StepFinishEvent{...})
            → handleTaskEvent in run() goroutine
              → ContextTokens = InputTokens + OutputTokens + CacheReadTokens + CacheCreationTokens (overwrite, only if non-zero)
```

Context tracking is handled by the `handleTaskEvent` method in `session_loop.go`, which processes `StepFinishEvent` events from the task goroutine:

```go
case StepFinishEvent:
    newContext := e.InputTokens + e.OutputTokens + e.CacheReadTokens + e.CacheCreationTokens
    if newContext > 0 {
        s.ContextTokens = newContext
    }
```

Note: `StepFinishEvent` carries only token usage metadata. The final message
state is returned separately via `taskResultCh` on task completion.

- **Overwrite (`Store`), not accumulate (`Add`).** Each API call's `InputTokens` already represents the *entire conversation history* sent in that request. Accumulating would double-count. `OutputTokens` is included because the model's `ContextLimit` is a combined input+output window, and the latest output is part of the conversation that will be sent in the next request.
- **Guard against zero reports.** Some OpenAI-compatible providers (e.g. GLM-5.1) may omit the `usage` field from SSE chunks entirely — they simply never send a chunk containing `"usage": {"prompt_tokens": N, ...}`. Go's `json.Unmarshal` leaves absent fields at their zero values, so the parsed `Usage` struct arrives as all zeros. Without the guard, this would reset `ContextTokens` to 0, breaking auto-summarization and the status bar display. The `if newContext > 0` check preserves the last known good value.
- **Only the last step's value matters.** For multi-step tool call loops, each step re-sends the full history (plus new messages). The last step has the most complete count.
- **Cross-goroutine communication.** The task goroutine sends usage via typed events on `taskEventCh`; the `run()` goroutine owns the authoritative copy and updates it via `handleTaskEvent`. The task goroutine reads `ContextTokens` in `shouldAutoSummarize` — since the taskEventCh drain in `handleTaskDone` commits all pending events before the next task starts, no atomic is needed.
- **Cache tokens are additive.** Anthropic reports `InputTokens` as the non-cached portion; `CacheReadTokens` and `CacheCreationTokens` are separate. The sum gives the true context size.

## Multi-Step Tool Calls

When the agent loop runs multiple steps (tool call → tool result → next step), `handleTaskEvent` is called once per step via `StepFinishEvent`. Each call overwrites `ContextTokens` with that step's full-context measurement (input + output + cache):

```
Step 1 (tool call):     InputTokens=500, OutputTokens=100, CacheRead=8000 → ContextTokens = 8600
Step 2 (tool response): InputTokens=900, OutputTokens=200, CacheRead=8000 → ContextTokens = 9100  ← final, correct value
```

The last step's value is always the most accurate because it includes all prior tool results and the latest output.

## Provider Differences

### Anthropic Protocol

Reports usage across multiple SSE events (`message_start`, `message_delta`, `message_stop`). The provider merges partial values: `InputTokens` and cache tokens come from `message_start`, `OutputTokens` from `message_delta`. If one event omits a field, the value from a prior event survives.

`InputTokens` = non-cached portion only. Cache tokens (`CacheReadTokens`, `CacheCreationTokens`) are separate and added together with `OutputTokens` in `handleTaskEvent` for the true context size.

### OpenAI Protocol

Reports usage in a **single SSE chunk** with `prompt_tokens` (full context, cache already included) and `completion_tokens`. No separate cache fields.

**This is a single point of failure.** If the provider never sends a chunk containing `"usage"`, the parsed `Usage` struct stays at all zeros. Some providers (e.g. GLM-5.1) intermittently omit the usage chunk. The `newContext > 0` guard in `handleTaskEvent` prevents resetting `ContextTokens` to 0.

## Model Switching and Token Count Changes

When switching models (e.g. Anthropic → OpenAI), the reported context size may change even though the conversation history is unchanged. This is expected — **different providers use different tokenizers**.

### Example

| Step | Provider | ContextTokens | API Reported (input, output, cache) |
|------|----------|--------------|-------------|
| Prompt 1, Step 1 (tool) | Anthropic/llama.cpp | 1199 | input=4, output=50, cache_read=1145 |
| Prompt 1, Step 2 (answer) | Anthropic/llama.cpp | 2218 | input=973, output=100, cache_read=1145 |
| Model switch | → OpenAI/glm-5.1 | *(unchanged)* | — |
| Prompt 2, Step 1 (tool) | OpenAI/glm-5.1 | 2123 | prompt_tokens=2073, completion_tokens=50 |
| Prompt 2, Step 2 (answer) | OpenAI/glm-5.1 | 2417 | prompt_tokens=2317, completion_tokens=100 |

The apparent "drop" from 2218 to 2123 after model switch is the difference in tokenization between the two models. The full conversation was sent correctly.

Note that `ContextTokens` now includes `OutputTokens`, so the values differ from the earlier documentation version where only input+cache were tracked.

## Manual Summarization (`:summarize`)

The `:summarize` command is a **task command** — it runs in a task goroutine and can be canceled with `:cancel`. It is the only way to reduce context usage manually when auto-summarize is disabled.

### What it does

1. Appends a structured summary prompt to the conversation history asking the LLM to condense everything into five sections:
   - **Task** — Original request and success criteria
   - **Done** — Completed items with specifics (file paths, function names, values)
   - **State** — Files created/modified/deleted, key decisions and rationale
   - **Blocked** — Unresolved errors, failing tests, open questions
   - **Next** — Ordered actions to resume
2. Calls the LLM to generate the summary
3. **Replaces the entire conversation history** with the summary (a "Continue" user message followed by the assistant's summary response)
4. Resets `ContextTokens` to the summary's output token count via `SetContextTokensEvent` (a dedicated event that corrects the value after the `StepFinishEvent` from `processPrompt` has been processed)

### ⚠️ Event ordering

During summarization, two task events are sent to the `run()` goroutine:
1. `StepFinishEvent` from `processPrompt` — sets `ContextTokens` to the full old-context token count
2. `SetContextTokensEvent` from `summarize` — corrects `ContextTokens` to the summary size

Both are sent by the same goroutine sequentially, and the FIFO channel guarantees the correction is processed last, so `ContextTokens` ends up at the correct value.

### ⚠️ Important caveats

- **Destructive** — The conversation history is replaced by the summary. Previous turns are lost. Only run `:summarize` when you're confident the summary captures everything needed.
- **One-shot** — There is no undo. Consider saving the session first (`:save`) if you might need the full history later.

### When to use

- **Auto-summarize is disabled** — Run it manually when the status bar shows high context usage.
- **Before switching tasks** — Summarize a completed task before starting a new one to keep context focused.
- **Before `:model_set`** — Different models use different tokenizers. Summarizing first ensures the new model receives a concise, consistent input.

## Related

- `shouldAutoSummarize()` — triggers when `ContextTokens >= ContextLimit * 65%` (only when `--auto-summarize` is enabled)
- `runSummarize()` (in `session_task.go`) — appends the summary prompt to Messages, calls `handleUserPrompt`, then replaces conversation history with the summary and resets `ContextTokens` to the summary's output token count via `SetContextTokensEvent`
- `SetContextTokensEvent` — a dedicated task event that sets `ContextTokens` to the correct value after summarization, overriding the stale value from the preceding `StepFinishEvent`
- `applyModelContextLimit()` — sets `ContextLimit` from the active model's config
- `SessionMeta.ContextTokens` — persisted to session file frontmatter so the status bar shows the correct context usage immediately after loading a session
