# Virtual Rendering Performance Analysis

Performance analysis of AlayaCore's virtual scrolling system for the terminal display.

## Summary

The virtual rendering system provides **3.5x speedup** for rendering operations. Render overhead is only ~1.4% of wall time during streaming — rate limiting is not needed.

All optimizations are working correctly:

- ✅ **Virtual rendering** — 3.5x faster than naive rendering
- ✅ **Incremental line height updates** — 130x faster than full rebuild
- ✅ **Incremental text wrapping** — 45x faster than full wrap
- ✅ **Fast path for cached content** — Uses cached `wrappedLines` when content hasn't changed

## Benchmark Results

### Virtual Rendering

| Scenario | Time | Speedup |
|----------|------|---------|
| `GetAll` with virtual rendering (100 windows) | ~17-21μs | **3.5x** |
| `GetAll` without virtual rendering (100 windows) | ~59-78μs | baseline |

### Incremental Line Height Updates

| Scenario | Time | Speedup |
|----------|------|---------|
| Incremental (1 dirty window) | ~41μs | **130x** |
| Full rebuild (all 100 windows) | ~5.3ms | baseline |

### Incremental Text Wrapping

| Scenario | Time | Speedup |
|----------|------|---------|
| Incremental append | ~1.6μs | **45x** |
| Full wrap | ~72μs | baseline |

### Cursor Movement

| Scenario | Time | Assessment |
|----------|------|------------|
| Single cursor move (average) | ~340μs | ✅ Fast (< 1ms) |
| Single cursor move (best case) | ~210μs | — |
| Single cursor move (worst case) | ~800μs | — |

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

1. **UI refresh is polled at 250ms intervals** (`terminal.go` → `TickInterval`) — data ingestion itself is not throttled
2. **Render overhead is only 1.4%** of wall time during streaming
3. **`updateContent()` skips unchanged content** efficiently
4. **Virtual rendering provides 3.5x speedup** when viewport is not at bottom
5. **Average update time is ~280-340μs** — well under 1ms
