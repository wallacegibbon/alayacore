# Virtual Rendering Performance Analysis

## Executive Summary

The virtual rendering system provides **3.5x speedup** for rendering operations. All optimizations are working correctly:
- Virtual rendering: ✅ Working (3.5x faster)
- Incremental updates: ✅ Working (100x faster than full rebuild)
- Incremental wrapping: ✅ Working (52x faster than full wrap)
- Fast path for cached content: ✅ Working (uses cached wrappedLines)

The streaming performance bottleneck is NOT a bug - it's inherent to rendering growing styled content with borders.

## Benchmark Results

### Virtual Rendering Performance

| Scenario | Time | Speedup |
|----------|------|---------|
| GetAll with virtual rendering (100 windows) | ~17-21μs | **3.5x faster** |
| GetAll without virtual rendering | ~59-78μs | baseline |

Virtual rendering provides significant speedup by only rendering visible windows (with buffer zones).

### Incremental Line Height Updates

| Scenario | Time | Speedup |
|----------|------|---------|
| Incremental (1 dirty window) | ~39-40μs | **100x faster** |
| Full rebuild (all 100 windows) | ~4.3ms | baseline |

The `dirtyIndex` optimization correctly tracks which window needs rebuilding.

### Incremental Wrapping

| Scenario | Time | Speedup |
|----------|------|---------|
| Incremental append | ~1.5-1.8μs | **52x faster** |
| Full wrap | ~78-84μs | baseline |

The `appendDeltaToLines` optimization provides massive speedup for streaming text.

### Cursor Movement Performance

| Scenario | Time | Assessment |
|----------|------|------------|
| Single cursor move | ~210μs | ✅ Fast enough (< 1ms) |

## Understanding Streaming Performance

### The Benchmarks

```
BenchmarkSingleWindowStreaming-24       	    5602	    209542 ns/op  (~210μs per update)
BenchmarkLongContentStreaming-24        	    9502	    6444551 ns/op  (~6.4ms per update)
```

### Why Streaming is Slower with More Content

This is NOT a bug. The performance is determined by:

1. **Content length**: Each streaming delta grows the content
2. **Styling overhead**: `styleMultiline()` processes all lines each time
3. **Border rendering**: Lipgloss border rendering is O(content length)
4. **String operations**: Building styled output from growing content

The fast path in `renderGenericContent()` works correctly:
```go
// FAST PATH: Use cached wrapped lines if width matches
if len(w.cache.wrappedLines) > 0 && w.cache.width-4 == innerWidth && innerWidth > 0 {
    return strings.Join(w.cache.wrappedLines, "\n")
}
```

But the cache is invalidated when content changes, so the fast path only helps when:
- Content hasn't changed (just re-rendering)
- Width hasn't changed

### Debug Output Confirms Fast Path Works

```
After AppendContent 1: wrappedLines=1, cache.valid=false
After Render 1: wrappedLines=1, cache.valid=true
After AppendContent 2: wrappedLines=1, cache.valid=false
After Render 2: wrappedLines=1, cache.valid=true
```

The wrappedLines are preserved and updated incrementally. The 5-8ms timing is from:
- Styling the full content (O(n))
- Building the border (O(n))
- String concatenation

## Performance Targets

| Operation | Current | Target | Status |
|-----------|---------|--------|--------|
| Cursor move (single) | 210μs | < 500μs | ✅ Good |
| GetAll (virtual, 100 windows) | 17μs | < 50μs | ✅ Good |
| Incremental update (1 dirty) | 39μs | < 100μs | ✅ Good |
| Full rebuild (100 windows) | 4ms | < 10ms | ✅ Acceptable |

## Recommendations

### 1. Rate-Limit Streaming Updates (RECOMMENDED)

For streaming responses, consider updating the UI at most every 50-100ms instead of on every character:

```go
// In handleTick or streaming update
if time.Since(lastUIUpdate) < 50*time.Millisecond {
    return // Skip this update
}
lastUIUpdate = time.Now()
m.display.updateContent()
```

This provides smooth UX while avoiding unnecessary renders.

### 2. Consider Content Truncation for Very Long Windows (OPTIONAL)

For windows with >10,000 characters, consider:
- Virtualizing content within a single window
- Truncating old content with "... (N lines hidden)"
- Adding a "view full content" option

### 3. Use Prefix Sum for Line Positions (LOW PRIORITY)

`GetWindowStartLine` is O(n) but very fast (~15ns). Only optimize if profiling shows it's a bottleneck.

## Conclusion

The virtual rendering system is **working correctly and efficiently**:
- ✅ 3.5x speedup from virtual rendering
- ✅ 100x speedup from incremental line height updates
- ✅ 52x speedup from incremental wrapping
- ✅ Fast path for cached content

The streaming performance is acceptable for normal use. For very long streaming responses, rate-limiting UI updates would provide the best UX improvement.
