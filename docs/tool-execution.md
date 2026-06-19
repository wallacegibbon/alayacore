# Tool Execution: Concurrent + Deferred

AlayaCore executes tool calls using a **two-phase strategy**: tools that don't need confirmation run **concurrently during streaming**, while tools needing confirmation are **deferred and executed as confirmations arrive**.

## How It Works

When a `ToolInputPart` event arrives during streaming, the agent calls `ToolNeedsConfirm(toolName)`:

1. **No confirmation needed** — The tool executes immediately in a goroutine. Results flow back through a channel and are appended in receive order.
2. **Confirmation needed** — The tool is deferred. After streaming completes, `OnToolConfirm` is called with only the deferred tools. As each confirm response arrives, the confirmed tool executes in a goroutine.

All results are collected and then re-ordered by tool call ID to match the original `stepMessage.Contents` order.

See `internal/llm/agent.go` → `Stream()`, `streamEvents()`, and `executeDeferredTools()`.

## Execution Strategy

| Phase | Tools | Execution |
|-------|-------|-----------|
| **During streaming** | No confirmation needed (`ToolNeedsConfirm` returns false) | Concurrent goroutines, results appended and re-ordered by ID |
| **After streaming** | Confirmation needed (deferred) | Concurrent goroutines per confirm response, results appended and re-ordered by ID |
| **Final** | All results | Re-ordered by tool call ID to match LLM response order |

## Confirmation

`ToolNeedsConfirm` filters which tools need user approval. Only those tools are deferred; all others execute immediately during streaming. `OnToolConfirm` receives only confirm-required tools and returns a channel. The agent reads confirm responses one at a time. As each tool is confirmed, it executes immediately in a goroutine — overlapping with other confirms and executions.

The TUI adapter processes confirmations sequentially (one dialog at a time). Other adapters can process the batch in parallel.

## Implementation

Results from both phases are merged and re-ordered by ID:

```go
toolUses := extractToolUses(stepMessage.Contents) // original order from LLM
idToTool := map ID → index
for _, r := range results {
    finalResults[idToTool[r.ID]] = r
}
```
