# Tool Execution: Parallel + Sequential Hybrid

AlayaCore executes tool calls using a **hybrid strategy**: read-only tools run **concurrently** while state-mutating tools run **sequentially**. This gives the best of both worlds — safe parallel execution for tools that are known to be safe, and ordered execution for everything else.

## How It Works

When the LLM returns multiple `tool_use` blocks in one response, the agent's `executeTools()` method:

1. **Confirms all tools** (if user confirmation is enabled) — sequentially, since this involves user interaction.
2. **Separates into groups** based on each tool's `SafeToConcur` flag.
3. **Runs parallel-safe tools concurrently** — via goroutines.
4. **Runs sequential tools one at a time** — in order.
5. **Collects all results** and sends them back to the LLM in a single tool result message.

All results are returned in the same order as the original tool calls, regardless of execution order.

See `internal/llm/agent.go` → `executeTools()`.

## Which Tools Are Parallel-Safe

| Tool | Side Effects | `SafeToConcur` | Why |
|------|-------------|----------------|-----|
| `read_file` | None | ✅ Yes | Read-only, no side effects |
| `search_content` | None | ✅ Yes | Read-only, no side effects |
| `edit_file` | Mutates files | ❌ No | Two edits on the same file would race |
| `write_file` | Creates/overwrites files | ❌ No | Concurrent writes to the same file would race |
| `execute_command` | Anything can happen | ❌ No | Commands may depend on each other's side effects |

## Why Not All Parallel?

The LLM frequently mixes reads and writes in the same response block. Running those in parallel creates subtle bugs — two `edit_file` calls on the same file, or an `execute_command` call that depends on a prior `write_file`, would produce unpredictable results.

The LLM returns multiple tool calls because the API format supports it, not because it has verified they are truly independent. Treating them as parallel-safe by default would be an incorrect assumption.

## Benefits

- **Reads are now faster** — `read_file` and `search_content` calls complete concurrently, reducing latency when the LLM explores multiple files in one step.
- **Writes remain safe** — File mutations and commands still execute one at a time.
- **Deterministic results** — The parallel group collects results via a channel and preserves input order.
- **Opt-in per tool** — A tool author explicitly marks a tool as `SafeToConcur`. The default is sequential.

## Implementation

The per-tool concurrency hint lives on the `Tool` struct:

```go
type Tool struct {
    Definition   ToolDefinition
    Execute      func(ctx context.Context, input json.RawMessage) (ToolResultOutput, error)
    SafeToConcur bool // opt-in per tool
}
```

Set via the builder:

```go
NewTool("read_file", "...").
    WithSchema(...).
    WithExecute(...).
    SafeToConcur().
    Build()
```
