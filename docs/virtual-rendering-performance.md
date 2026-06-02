# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

## Summary

The virtual rendering system provides **~3.2x speedup** for rendering operations. Render overhead is only ~1.4% of wall time during streaming — rate limiting is not needed. Content append is O(1) per delta and line counting during streaming skips the expensive lipgloss border render (~58μs).

All optimizations are working correctly:

- ✅ **Virtual rendering** — 3.2x faster than naive rendering
- ✅ **Incremental line height updates** — 128x faster than full rebuild
- ✅ **Incremental content append** — O(1) per delta, avoids O(n²) string copy
- ✅ **Fast path for cached content** — Uses cached `wrappedLines` when content hasn't changed
- ✅ **UpdateLineCount skips border render** — Line counting during streaming avoids ~58μs lipgloss border render

## Benchmark Results

### Virtual Rendering

| Scenario | Time | Speedup |
|----------|------|---------|
| `GetAll` with virtual rendering (100 windows) | ~21μs | **3.2x** |
| `GetAll` without virtual rendering (100 windows) | ~69μs | baseline |

### Incremental Line Height Updates

| Scenario | Time | Speedup |
|----------|------|---------|
| Incremental (1 dirty window) | ~41μs | **128x** |
| Full rebuild (all 100 windows) | ~5.3ms | baseline |

### Incremental Text Wrapping

| Scenario | Time | Speedup |
|----------|------|---------|
| Incremental append (1 window) | ~64μs | **1.4x** |
| Full wrap (1 window) | ~88μs | baseline |

The wrapping speedup appears modest in isolation because both paths wrap roughly the same amount of text for a single window. The real benefit of the incremental path is that it updates wrapped lines in **O(delta)** per append via `appendDeltaToLines`, avoiding the **O(n²)** cost of `Content += delta` string concatenation over a long streaming session. After 10,000 streaming deltas, the incremental path has copied ~10KB total vs ~50MB with naive string concatenation.

### Cursor Movement

| Scenario | Time | Assessment |
|----------|------|------------|
| Single cursor move (average) | ~325μs | ✅ Fast (< 1ms) |

## Streaming Performance

### Realistic Streaming Test (50ms word intervals)

```
Words streamed:     17
Simulated interval: 50ms
Total wall time:    850ms
Total render time:  12.0ms
Average render:     707μs
Render overhead:    1.41%
```

### High-Frequency Updates (no sleep)

```
Total updates:      100
Total time:         28.0ms
Average per update: 280μs
Updates per second: 3572
```

## Why Rate Limiting Isn't Needed

1. **UI refresh is polled at 250ms intervals** (`tui.go` → `TickInterval`) — data ingestion itself is not throttled
2. **Render overhead is only ~1.4%** of wall time during streaming
3. **`updateContent()` skips unchanged content** efficiently
4. **Virtual rendering provides ~3.2x speedup** when viewport is not at bottom
5. **Average update time is ~280-340μs** — well under 1ms
