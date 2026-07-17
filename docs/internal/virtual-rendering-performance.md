# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

Benchmarks run on: Intel(R) Core(TM) Ultra 9 285K, Linux amd64.

## Summary

All optimizations are working correctly:

- ✅ **Virtual rendering** — 3.7x faster than naive rendering
- ✅ **Incremental content append** — O(delta) per frame via `appendDeltaToLines`, avoids O(n) full re-wrap (~511x speedup on 5000-line content)
- ✅ **Incremental line height tracking** — `TryLineCount` from `wrappedLines` in ~7.5μs (full `ensureLineHeights` with 1 dirty window), no full render needed
- ✅ **Streaming stays under 1ms** — average 24.8μs per full cycle (append + line tracking + GetAll), well within 250ms tick budget
- ✅ **Custom ScrollView** (<1KB) replaces Bubbles viewport — View() is **~540–560ns** (down from ~10μs), 6 allocs per call

## How Streaming Works

During streaming, every `AppendFromTLV` call on a `textRenderer`:

1. Appends the delta to `contentParts` (O(1) for eventual consistency)
2. Styles the delta via `styleByTag` and wraps it via `appendDeltaToLines`
3. Updates `wrappedLines` **incrementally** — only the new text is wrapped and appended
4. `TryLineCount` returns `len(wrappedLines) + 2` immediately — no render needed

This means line tracking during streaming is **always fast**, not just on cache hits:

```
Streaming frame arrives → appendDeltaToLines (O(delta))
TryLineCount → len(wrappedLines) + 2  (~7.5μs via ensureLineHeights, no full render)
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
| Average full cycle (append + line tracking + GetAll) | **24.8μs** |
| Incremental append only (AppendOrUpdate) | **58ns** |
| Small delta streaming (append + line tracking) | **6.4μs** |
| Long content incremental append (5000-line content) | **6.4μs** |
| Budget | < 1ms (target), 250ms (actual tick) |

### Incremental Append vs Full Re-wrap (5000-line content)

Measured via `BenchmarkAppendVsFullWrap_LongContent` (500 lines of wrapped content, ~5000 wrapped lines at 80 cols).

| Operation | Time | Memory | Allocs |
|-----------|------|--------|--------|
| **Incremental append** | **6.4μs** | **3.2KB** | **268** |
| Full re-wrap | 3.27ms | 1.97MB | 69,996 |
| **Speedup** | **511x** | **608x** | **261x** |

Without the incremental path, every streaming frame on a long LLM response
would trigger a full O(n) re-wrap of the entire accumulated content — 3.27ms
per frame. At the 250ms tick interval this is still manageable, but burst
scenarios (multiple frames arriving between ticks) would accumulate latency.

### Streaming Update End-to-End (100 windows, 50 history + 1 streaming)

Measured via `BenchmarkStreamingUpdateWithIncremental` vs `BenchmarkStreamingUpdateWithoutIncremental`.

| Scenario | Time | Memory | Allocs | Speedup |
|----------|------|--------|--------|:-------:|
| **Incremental (1 dirty window)** | **24.8μs** | 101KB | 308 | **baseline** |
| Full rebuild (all dirty, no incremental) | 2.95ms | 1.87MB | 64,570 | **119x slower** |

### Virtual Rendering

Measured via `BenchmarkGetAllWithVirtual` vs `BenchmarkGetAllWithoutVirtual` (100 windows, viewport=30 lines).

| Scenario | Time | Memory | Speedup |
|----------|------|--------|:-------:|
| `GetAll` with virtual rendering (100 windows) | **25μs** | 151KB | **3.7x** |
| `GetAll` without virtual rendering (100 windows) | **92μs** | 510KB | baseline |

### Line Height Tracking

Measured via `BenchmarkJustEnsureLineHeights` (20 windows, 1 dirty window, cached path).

| Scenario | Time | Notes |
|----------|------|-------|
| Incremental (1 dirty window, cached) | **7.5μs** | `ensureLineHeights` via `TryLineCount` from `wrappedLines` |
| Incremental (1 dirty window, uncached) | ~150μs* | Falls through to full `Render()` |
| Full rebuild (all 100 windows) | ~7.1ms* | All windows rendered from scratch |

\* Historical estimates — measured on earlier hardware/configuration.

### Full Update Cycle (Delta + GetAll)

Measured via `BenchmarkWindowBufferDeltaWithGetAll` (100 windows, delta to last window).

| Metric | Value |
|--------|-------|
| Delta + GetTotalLines + GetAll (incremental) | **31.2μs** |
| Delta + GetTotalLines + GetAll (full rebuild) | **5.3ms*** |

\* Historical comparison — incremental is ~170x faster.

### Cursor Movement

Measured via `BenchmarkVirtualRenderingCursorMovementSingle` (100 windows, viewport=30).

| Metric | Value |
|--------|-------|
| Single cursor move (EnsureCursorVisible + updateContent) | **77μs** |
| Scroll 20 steps down + 20 steps up | **611μs** |

### GetWindowLineRange

| Scenario | Time |
|----------|------|
| Single lookup (windowIndex=50, 100 windows) | **23ns** |
| Cached (3 lookups) | **56ns total** |

### ScrollView Component

| Metric | Value |
|--------|-------|
| `View()` (n=10 to n=10000) | **540–562ns**, 6 allocs |
| `ScrollDown(1)` | **8.6ns**, 0 allocs |
| `WithContent(n=10000)` | **93.6μs**, 1 alloc |

### wrapContent vs lipgloss.Wrap

| Algorithm | Time | Memory | Allocs |
|-----------|------|--------|--------|
| **wrapContent** (character-boundary) | **28.4μs** | 17.3KB | 1,780 |
| lipgloss.Wrap (word-boundary) | 36.0μs | 15.7KB | 1,781 |
| **Speedup** | **1.27x** | — | — |

### Resize Performance

| Scenario | Time |
|----------|------|
| Resize 50 windows (80↔120 cols) | **2.75ms** |

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** — data ingestion itself is not throttled
2. **Render overhead is well under 0.01%** of wall time during streaming (24.8μs per 250ms tick = 0.01%)
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
If the renderer's `wrappedLines` is populated, this returns the line count in ~7.5μs
without rendering. The actual `w.Render()` — which joins wrapped lines, applies borders,
and renders lipgloss styles — is deferred to `GetAll` → `renderVirtual`, which needs
the rendered output for the viewport anyway.
