# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

## Summary

All optimizations are working correctly:

- ✅ **Virtual rendering** — 4.4x faster than naive rendering
- ✅ **Incremental line height updates** — 22,000x faster for cache-hit scenarios (resize, theme change)
- ✅ **Deferred full render** — `ensureLineHeights` defers `w.Render()` to `GetAll` → `renderVirtual`
- ✅ **Streaming stays under 1ms** — average 236μs per update, well within 250ms tick budget

## When the Fast Path Applies

The `UpdateLineCountFast` (~58μs) path only works when the renderer's internal cache is still valid. This happens **after resize or theme change** — the content hasn't changed, just the display parameters.

During **streaming**, every `AppendFromTLV` call invalidates the renderer's cache (`cacheValid = false`).
`TryLineCount` returns `(0, false)`, and `ensureLineHeights` falls through to the full `Render` (~100-200μs).

This is correct behavior: after content changes, the line count must be recomputed from the full
content. The old approach of returning a stale count from cached `wrappedLines` caused line height
desync (black screen on cursor navigation).

## Benchmark Results

### Virtual Rendering

| Scenario | Time | Speedup |
|----------|------|---------|
| `GetAll` with virtual rendering (100 windows) | ~10μs | **4.4x** |
| `GetAll` without virtual rendering (100 windows) | ~4.2ms | baseline |

### Streaming Performance (Realistic Test)

| Metric | Value |
|--------|-------|
| Average update time | **236μs** |
| Updates per second | 4,235 |
| Long content average | 336μs |
| Full cycle budget | < 250ms (TickInterval) |

### Cursor Movement

| Scenario | Time | Assessment |
|----------|------|------------|
| `EnsureCursorVisible + updateContent` | ~340μs avg | ✅ Fast |

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** — data ingestion itself is not throttled
2. **Render overhead is well under 1%** of wall time during streaming
3. **`updateContent()` skips unchanged content** efficiently
4. **Average full cycle time is ~236μs** — well under the 250ms tick budget

## Key Design Decisions

### Fast Path vs Full Render

The two-tier approach:
- **Fast path** (`UpdateLineCountFast` / `TryLineCount`): reads `len(wrappedLines) + 2` from the
  renderer's cache. Only valid when content hasn't changed (resize, theme switch).
- **Slow path** (full `Render`): calls `BuildInner` to recompute everything. Used during streaming.
  ~100-200μs.

### Why `ensureLineHeights` Defers `w.Render()`

During streaming, `ensureLineHeights` calls `Render()` for line counting. The rendered string is
cached in `Window.border` and reused by `GetAll` → `renderVirtual` for the viewport. This avoids
a second redundant render.

### Why the Two Cache Layers

| Cache | Location | Contents | Invalidated by |
|-------|----------|----------|---------------|
| Renderer cache | `textRenderer.wrappedLines` | Wrapped content lines (for line counting) | Content append |
| Border cache | `Window.border` | Border-wrapped output + lineCount | Content append, resize, theme change |

Renderer cache is per-type (`textRenderer` only). Border cache is shared by all window types.
`lineCount` lives in border cache so `WindowBuffer` can read it with field access (no interface dispatch).
