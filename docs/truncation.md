# Text Truncation

How AlayaCore truncates text output to stay within context budgets while preserving UTF-8 integrity.

## Overview

The `internal/truncation` package provides shared text truncation utilities used across the codebase. All functions guarantee valid UTF-8 output — they never split a multi-byte character.

Two truncation strategies are available, each suited to different use cases:

| Strategy | Keeps | Use Case |
|----------|-------|----------|
| `Lines` | First N non-empty lines | Line-oriented output (search results) |
| `Front` | Front of text within byte budget | Command output |

## Lines Strategy

`Lines(input string, maxLines int) (bool, string)` returns the first `maxLines` non-empty lines from input.

### Behavior

- Skips empty lines (they don't count toward the limit)
- Returns `(wasTruncated, result)` tuple
- Never slices by byte offset — uses `bufio.Scanner` to read complete lines
- Each line in output is an intact, valid-UTF-8 substring

### Usage

Used by `search_content` tool for ripgrep output:

```go
truncated, output := truncation.Lines(input, maxLines)
```

Default limit is 100 lines, configurable via the `max_lines` parameter.

Ripgrep returns all matches; truncation enforces a global line limit. This is simpler than using `--max-count` (which limits per-file) and provides predictable, consistent output.

### Example

```
Input (10 lines, 3 empty):
  "line1\n\nline2\nline3\n\n\nline4\nline5\n\nline6\n"

Lines(input, 3):
  wasTruncated = true
  output = "line1\nline2\nline3"
```

Empty lines are skipped, so the first 3 non-empty lines are returned.

## Front Strategy

`Front(text string, budget int, marker string) string` truncates text so the total output (content + marker) fits within `budget` bytes.

### Behavior

- Each rune pays its **actual byte cost** (`utf8.RuneLen`)
- ASCII characters: 1 byte each
- CJK characters: 3 bytes each
- Emoji: 4 bytes each
- Marker `"\n[truncated]"` (12 bytes) is appended when truncation occurs

### Budget Calculation

```
contentBudget = totalBudget - len(marker)
```

If `contentBudget <= 0`, returns only the marker.

### Usage Locations

| Location | Budget | Description |
|----------|--------|-------------|
| `execute_command` | 32 KB | Command output truncation |

### Example

```go
// 32KB budget with default marker
output := truncation.Front(text, 32*1024, truncation.Marker)
```

### CJK Fairness

The byte budget approach ensures fair handling of all scripts:

| Text | Characters | Bytes | Result |
|------|------------|-------|--------|
| `"Hello world"` | 11 | 11 | Fits in 100-byte budget |
| `"你好世界"` | 4 | 12 | Each CJK char = 3 bytes |
| `"🎉🎊🎈"` | 3 | 12 | Each emoji = 4 bytes |

With a 10-byte budget:
- `"Hello world"` → `"[truncated]"` (11 bytes exceeds 10-byte budget, marker only)
- `"你好世界"` → `"[truncated]"` (12 bytes exceeds 10-byte budget, marker only)

This approach avoids the approximation of "1 token ≈ 4 characters" which is wildly inaccurate for non-ASCII scripts.

## Marker Format

```go
const Marker = "\n[truncated]"
```

The marker serves two purposes:

1. **LLM Awareness**: The LLM knows content was omitted and can re-read files if needed
2. **Debugging**: Humans can see where truncation occurred

The leading newline ensures the marker appears on its own line, not appended to the last line of content.

## Where Truncation is Applied

### Tool Output

| Tool | Strategy | Limit | Configurable |
|------|----------|-------|--------------|
| `execute_command` | Front | 32 KB | No |
| `search_content` | Lines | 100 lines default | Yes (`max_lines` param) |
| `read_file` | None | 32 KB max (returns error; use `start_line`/`end_line` for large files) | No |

Note: `search_content` uses only truncation for limiting output. Ripgrep returns all matches; the global `max_lines` parameter controls total output. This is simpler than using ripgrep's `--max-count` (which limits per-file) and provides predictable, consistent results.

### History Compaction

`compactHistory()` compacts old messages to save context tokens. In long agent sessions, tool results accumulate and consume increasing amounts of context.

`compactHistory()` is called after each user prompt completes. Messages older than the last 3 steps are compacted using one strategy:

1. **Tool call/result pairs** — removed entirely from old messages. The model can re-invoke the tool if it needs the data. Error results are preserved since they're small and actionable. Skill directory reads are also preserved. Preserved calls keep their full input for debugging context.

Reasoning (chain of thought) is **never stripped** — it cannot be reconstructed and is essential for multi-step reasoning continuity.

```
Before compaction (10 messages, 5 steps):
  [user] [assistant: reasoning + text + toolcall] [tool: 15KB result]
  [assistant: reasoning + text + toolcall]        [tool: 20KB result]
  [assistant: reasoning + text + toolcall]        [tool: 8KB result]
  [assistant: reasoning + text + toolcall]        [tool: result]
  [assistant: reasoning + text]

After compaction:
  [user] [assistant: reasoning + text]
         [assistant: reasoning + text]
         [assistant: reasoning + text + toolcall] [tool: 8KB result]  ← kept full
         [assistant: reasoning + text + toolcall] [tool: result]      ← kept full
         [assistant: reasoning + text]                                ← kept full
```

| Setting | Flag |
|---------|------|
| Disable | `--no-compact` |

### Skill Directory Exemption

Files under skill directories are **exempt from compaction** in `compactHistory()`:

- `SKILL.md` — skill instructions
- `scripts/` — executable scripts
- `references/` — reference documents
- `assets/` — supporting files

This ensures skill instructions remain intact for the LLM to follow correctly.

## UTF-8 Safety Guarantee

All truncation functions guarantee valid UTF-8 output:

### Lines

- Uses `bufio.Scanner` which reads complete lines
- Never slices by byte offset
- Each output line is an intact substring

### Front

- Uses `utf8.RuneLen(r)` to get each rune's byte cost
- Cuts at rune boundaries, never mid-character
- Even 4-byte emoji are handled correctly

```go
// Walk runes, accumulate actual byte cost, find the cut point.
cut := 0
for _, r := range text {
    runeLen := utf8.RuneLen(r)
    if cut+runeLen > contentBudget {
        break
    }
    cut += runeLen
}
return text[:cut] + marker
```

The slice `text[:cut]` is always valid UTF-8 because `cut` is accumulated by whole rune lengths.

## API Reference

### Lines

```go
func Lines(input string, maxLines int) (bool, string)
```

**Parameters:**
- `input`: Text to truncate
- `maxLines`: Maximum non-empty lines to keep

**Returns:**
- `bool`: `true` if truncation occurred
- `string`: Truncated output (no marker appended)

### Front

```go
func Front(text string, budget int, marker string) string
```

**Parameters:**
- `text`: Text to truncate
- `budget`: Maximum total bytes (content + marker)
- `marker`: String to append when truncated (use `truncation.Marker`)

**Returns:**
- `string`: Truncated output with marker, or original text if it fits

### Marker Constant

```go
const Marker = "\n[truncated]" // 12 bytes
```

## Related

- [Context Tracking](context-tracking.md) — How context tokens are tracked across API calls
- [Error Handling](error-handling.md) — `max_tokens` truncation vs. errors
