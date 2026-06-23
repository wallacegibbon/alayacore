# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

## Summary

All optimizations are working correctly:

- ✅ **Virtual rendering** — 4.4x faster than naive rendering
- ✅ **Incremental content append** — O(delta) per frame via `appendDeltaToLines`, avoids O(n) full re-wrap
- ✅ **Incremental line height tracking** — `TryLineCount` reads `len(wrappedLines)` in ~58μs, no full render needed
- ✅ **Streaming stays under 1ms** — average 236μs per full cycle, well within 250ms tick budget

## How Streaming Works

During streaming, every `AppendFromTLV` call on a `textRenderer`:

1. Appends the delta to `contentParts` (O(1) for eventual consistency)
2. Styles the delta via `styleByTag` and wraps it via `appendDeltaToLines`
3. Updates `wrappedLines` **incrementally** — only the new text is wrapped and appended
4. `TryLineCount` returns `len(wrappedLines) + 2` immediately — no render needed

This means line tracking during streaming is **always fast**, not just on cache hits:

```
Streaming frame arrives → appendDeltaToLines (O(delta))
TryLineCount → len(wrappedLines) + 2  (~58μs, no full render)
```

A dedicated assertion test (`TestIncrementalPathIsUsed`) verifies that
`TryLineCount` returns a valid count after every delta append. If the
incremental path breaks, this test fails immediately.

### When Full Re-wrap Happens

Full `wrapContent` from scratch only occurs when:

| Event | Why |
|-------|-----|
| **Terminal resize** | Width changed, all lines must be re-wrapped at new width |
| **Theme switch** | Styles changed, all lines must be re-styled and re-wrapped |
| **First render** | No cached wrappedLines yet |

During normal streaming, none of these happen — incremental path is used
exclusively.

## Benchmark Results

### Streaming Performance (Realistic 250ms Tick)

| Metric | Value |
|--------|-------|
| Average full cycle (append + line tracking + GetAll) | **236μs** |
| Long content full cycle | 336μs |
| Budget | < 1ms (target), 250ms (actual tick) |

### Incremental Append vs Full Re-wrap (5000-line content)

| Operation | Time | Memory | Allocs |
|-----------|------|--------|--------|
| **Incremental append** | **7.4μs** | **3.2KB** | **267** |
| Full re-wrap | 3.24ms | 1.95MB | 67,646 |
| **Speedup** | **436x** | **616x** | **253x** |

Without the incremental path, every streaming frame on a long LLM response
would trigger a full O(n) re-wrap of the entire accumulated content — 3.24ms
per frame. At the 250ms tick interval this is still manageable, but burst
scenarios (multiple frames arriving between ticks) would accumulate latency.

### Virtual Rendering

| Scenario | Time | Speedup |
|----------|------|---------|
| `GetAll` with virtual rendering (100 windows) | ~10μs | **4.4x** |
| `GetAll` without virtual rendering (100 windows) | ~4.2ms | baseline |

### Line Height Tracking

| Scenario | Time | Notes |
|----------|------|-------|
| Incremental (1 dirty window, cached) | ~58μs | `TryLineCount` from wrappedLines |
| Incremental (1 dirty window, uncached) | ~150μs | Falls through to full `Render()` |
| Full rebuild (all 100 windows) | ~7.1ms | All windows rendered from scratch |

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** — data ingestion itself is not throttled
2. **Render overhead is well under 1%** of wall time during streaming
3. **`updateContent()` skips unchanged content** efficiently
4. **Incremental append is O(delta)** — no quadratic accumulation for long responses

## Key Design Decisions

### Incremental `appendDeltaToLines`

`textRenderer.AppendFromTLV` converts each delta to styled text and passes it to
`appendDeltaToLines`, which only wraps the delta and appends it to the existing
`wrappedLines` slice. This avoids re-wrapping the entire accumulated content.

The old approach (before the `WindowRendering` interface refactoring) used the same
optimization. It was accidentally dropped during the refactoring and restored in
commit `1021326`.

### Two-Tier Caching

| Cache | Location | Contents | Invalidated by |
|-------|----------|----------|---------------|
| Renderer lines | `textRenderer.wrappedLines` | Wrapped styled lines | Resize, theme change |
| Border output | `Window.border` | Border-wrapped output + lineCount | Content append, resize, theme |

Renderer lines are **updated incrementally** during streaming (not invalidated).
Border cache is marked invalid on every content change but rebuilt on next render.

`lineCount` lives in border cache so `WindowBuffer` can read it with direct field
access (no interface dispatch on the hot path).

### Why `ensureLineHeights` Defers Full Render

During streaming, `ensureLineHeights` first tries `UpdateLineCountFast` → `TryLineCount`.
If the renderer's `wrappedLines` is populated, this returns the line count in ~58μs
without rendering. The actual `w.Render()` — which joins wrapped lines, applies borders,
and renders lipgloss styles — is deferred to `GetAll` → `renderVirtual`, which needs
the rendered output for the viewport anyway.
