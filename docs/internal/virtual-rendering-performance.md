# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

## Summary

The virtual rendering system provides **~4.4x speedup** for rendering operations. Render overhead is well under 1% of wall time during streaming. Content append is O(1) per delta and line counting during streaming uses `len(wrappedLines)` instead of joining all lines, eliminating the O(n) string join on every update.

All optimizations are working correctly:

- ✅ **Virtual rendering** — 4.4x faster than naive rendering
- ✅ **Incremental line height updates** — **22,000x faster** than full rebuild (350ns vs 7.1ms)
- ✅ **Incremental content append** — O(1) per delta, avoids O(n²) string copy
- ✅ **Fast path for cached content** — Uses cached `wrappedLines` when content hasn't changed
- ✅ **UpdateLineCount skips border render** — Line counting during streaming uses `len(wrappedLines)` instead of join+count, eliminating the O(n) string concatenation on every update
- ✅ **Deferred full render** — `ensureLineHeights` defers `w.Render()` to `GetAll` → `renderVirtual`, avoiding a redundant render that would be immediately overwritten

## Benchmark Results

### Virtual Rendering

| Scenario | Time | Speedup |
|----------|------|---------|
| `GetAll` with virtual rendering (100 windows) | ~952μs | **4.4x** |
| `GetAll` without virtual rendering (100 windows) | ~4.2ms | baseline |

### Incremental Line Height Updates

| Scenario | Time | Speedup |
|----------|------|---------|
| Incremental (1 dirty window) | **0.35μs** | **22,000x** |
| Full rebuild (all 100 windows) | ~7.1ms | baseline |

The incremental path went from ~65μs to **0.35μs** (187x faster than the previous incremental implementation) because `UpdateLineCount` no longer joins all wrapped lines to count them — it simply returns `len(wrappedLines) + 2` (accounting for top/bottom border lines). The redundant `w.Render()` call was also removed from the incremental path since the render happens in `GetAll` anyway.

### UpdateLineCount Optimization

| Approach | Time | Allocations | Memory |
|----------|------|-------------|--------|
| **Old**: `strings.Join(wrappedLines, "\n")` + `strings.Count` | ~1,690μs | 17,437 allocs | 663KB |
| **New**: `len(wrappedLines) + 2` | **~7.9μs** | **87 allocs** | **0.95KB** |
| **Speedup** | **214x** | **200x** | **700x** |

For a 200-line window with ANSI-styled wrapped lines (~150 bytes each), the old approach joined a ~30KB string just to count to 200. The new approach reads a single integer.

### Delta Updates (100 Windows, Append to Last)

| Scenario | Time | Allocations | Memory |
|----------|------|-------------|--------|
| `WindowBufferDelta` | **5.8μs** | 91 allocs | 968B |
| `WindowBufferDeltaWithGetAll` | **705μs** | 8,289 allocs | 296KB |

Delta updates are extremely fast (5.8μs) due to incremental wrappedLines updates and the O(1) line count.

### Full Streaming Cycle (200-line window)

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| Time | 3,708μs | **1,619μs** | **2.3x faster** |
| Memory | 1.68MB | **818KB** | **2x less** |
| Allocations | 39,175 | **19,624** | **2x fewer** |

### Cursor Movement

| Scenario | Time | Assessment |
|----------|------|------------|
| Single cursor move (average) | ~1.2ms | ✅ Fast (< 2ms) |

## Streaming Performance

### Realistic Streaming Test (50ms word intervals)

Based on the tick interval of 250ms (`TickInterval` in `tui.go`), the render cycle processes at most 4 updates per second. Each full cycle (append + line tracking + GetAll) completes in ~1.6ms for a 200-line window — well under the 250ms budget.

### High-Frequency Updates

```
Delta updates per second: ~172,000 (just append + line tracking)
Full cycles per second:   ~617 (append + line tracking + GetAll)
```

## Key Design Decisions

### Why `UpdateLineCount` Uses `len(wrappedLines)` Instead of Join+Count

`wrappedLines` stores one element per wrapped content line. Counting wrapped lines is equivalent to reading `len(wrappedLines)`. The old approach joined all wrapped lines into a single string and counted newlines — an O(n) string concatenation that allocated hundreds of kilobytes on every update.

The total rendered line count includes 2 border lines (top and bottom) added by lipgloss's `RoundedBorder`. So the formula is:

```go
lineCount = len(wrappedLines) + 2  // non-folded
lineCount = min(5, len(wrappedLines)) + 2  // folded (max 5 content lines)
```

### Why `ensureLineHeights` Defers `w.Render()`

During streaming, `AppendContent` keeps `wrappedLines` populated via incremental `appendDeltaToLines`. The line count can be read from `len(wrappedLines)` without a full render. The actual `w.Render()` call — which joins wrapped lines, applies borders, and renders lipgloss styles — is deferred to `GetAll` → `renderVirtual`, which needs the rendered output for the viewport anyway. This avoids an O(n) render in `ensureLineHeights` that would be immediately overwritten.

### Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** (`tui.go` → `TickInterval`) — data ingestion itself is not throttled
2. **Render overhead is well under 1%** of wall time during streaming
3. **`updateContent()` skips unchanged content** efficiently
4. **Virtual rendering provides ~4.4x speedup** when viewport is not at bottom
5. **Average full cycle time is ~1.6ms** for a 200-line window — well under the 250ms tick budget
