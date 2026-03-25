package terminal

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// BenchmarkWindowBufferDelta benchmarks the performance of delta updates.
// Before the fix, each delta would re-render all windows (O(n)).
// After the fix, each delta only re-renders the dirty window (O(1)).
//
// Note: The content grows on each iteration, so word-wrapping cost increases.
// This measures the worst-case scenario where the last window keeps growing.
func BenchmarkWindowBufferDelta(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows (simulating a long conversation)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Pre-render to ensure everything is cached
	_ = wb.GetTotalLinesVirtual()

	// Get the last window's ID for delta updates
	lastID := "msg99"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta update - append to existing window
		wb.AppendOrUpdate(lastID, "TA", " additional text")

		// Trigger rendering (what happens on each update)
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkWindowBufferDeltaNewWindow tests updating a NEW window each time.
// This measures the cost of rendering one window without the growing content issue.
func BenchmarkWindowBufferDeltaNewWindow(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 initial windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Pre-render to ensure everything is cached
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create a NEW window each time (simulates new message in conversation)
		id := fmt.Sprintf("newmsg%d", b.N*1000+i)
		wb.AppendOrUpdate(id, "TA", "New message content.\n")

		// Trigger rendering
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkWindowBufferDeltaSingleWindow benchmarks delta updates with only one window.
// This should be fast regardless of the optimization.
func BenchmarkWindowBufferDeltaSingleWindow(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Single window
	wb.AppendOrUpdate("msg0", "TA", strings.Repeat("Initial content\n", 10))

	// Pre-render
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate("msg0", "TA", " delta")
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkWindowBufferGetWindowStartLine benchmarks the GetWindowStartLine function.
// This has O(n) behavior that could be optimized with a prefix sum array.
func BenchmarkWindowBufferGetWindowStartLine(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content line\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Ensure line heights are calculated
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Access middle window (worst case for O(n) loop)
		_ = wb.GetWindowStartLine(50)
	}
}

// BenchmarkWindowBufferGetAll benchmarks the GetAll function with virtual rendering.
func BenchmarkWindowBufferGetAll(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport for virtual rendering
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1)
	}
}

// BenchmarkWindowBufferDeltaWithGetAll benchmarks the full update cycle including GetAll.
func BenchmarkWindowBufferDeltaWithGetAll(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows (simulating a long conversation)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport for virtual rendering
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	// Get the last window's ID for delta updates
	lastID := "msg99"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta update
		wb.AppendOrUpdate(lastID, "TA", " additional text")

		// Full render cycle
		_ = wb.GetTotalLinesVirtual()
		_ = wb.GetAll(-1)
	}
}

// BenchmarkVirtualRenderingCursorMovement benchmarks cursor movement with virtual rendering.
// This tests the EnsureCursorVisible + updateContent path.
func BenchmarkVirtualRenderingCursorMovement(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)
	dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate moving cursor through all windows
		for j := 0; j < 100; j++ {
			dm.SetWindowCursor(j)
			dm.EnsureCursorVisible()
			dm.updateContent()
		}
	}
}

// BenchmarkVirtualRenderingCursorMovementSingle tests a single cursor move (more realistic)
func BenchmarkVirtualRenderingCursorMovementSingle(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)
	dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Single cursor move (realistic user action)
		dm.SetWindowCursor(i % 100)
		dm.EnsureCursorVisible()
		dm.updateContent()
	}
}

// BenchmarkStreamingUpdateWithIncremental uses the incremental path properly
func BenchmarkStreamingUpdateWithIncremental(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 50 existing windows (conversation history)
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	// Create streaming window with initial content
	streamID := "stream-current"
	wb.AppendOrUpdate(streamID, "TA", "Starting...")

	// Pre-render to populate wrappedLines cache
	_ = wb.GetTotalLinesVirtual()
	_ = wb.GetAll(-1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate streaming delta - this should use incremental wrapping
		wb.AppendOrUpdate(streamID, "TA", " more")
		_ = wb.GetTotalLinesVirtual()
		_ = wb.GetAll(-1)
	}
}

// BenchmarkStreamingUpdateWithoutIncremental forces full re-wrap
func BenchmarkStreamingUpdateWithoutIncremental(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 50 existing windows
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	// Create streaming window
	streamID := "stream-current"
	wb.AppendOrUpdate(streamID, "TA", "Starting...")
	_ = wb.GetTotalLinesVirtual()
	_ = wb.GetAll(-1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Force full re-wrap by invalidating wrappedLines BEFORE append
		wb.mu.Lock()
		if idx, ok := wb.idIndex[streamID]; ok {
			wb.Windows[idx].cache.wrappedLines = nil
		}
		wb.mu.Unlock()

		wb.AppendOrUpdate(streamID, "TA", " more")
		_ = wb.GetTotalLinesVirtual()
		_ = wb.GetAll(-1)
	}
}

// BenchmarkStreamingSmallDelta tests with small streaming deltas
func BenchmarkStreamingSmallDelta(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 existing windows
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "")
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate(streamID, "TA", "word ")
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkJustAppendUpdate isolates the AppendOrUpdate cost
func BenchmarkJustAppendUpdate(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 existing windows
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate(streamID, "TA", "word ")
	}
}

// BenchmarkJustEnsureLineHeights isolates the ensureLineHeights cost
func BenchmarkJustEnsureLineHeights(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 existing windows
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "initial")
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		wb.AppendOrUpdate(streamID, "TA", " word")
		wb.dirty = true
		wb.dirtyIndex = wb.idIndex[streamID]
		b.StartTimer()

		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkStreamingDebug shows why streaming is slow
func BenchmarkStreamingDebug(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", strings.Repeat("Line ", 10))
	_ = wb.GetTotalLinesVirtual()

	w := wb.Windows[0]
	fmt.Printf("Initial: wrappedLines=%d, Content=%d, cache.contentLen=%d\n",
		len(w.cache.wrappedLines), len(w.Content), w.cache.contentLen)

	for i := 0; i < 3; i++ {
		wb.AppendOrUpdate(streamID, "TA", " more")
		fmt.Printf("\nAfter AppendOrUpdate %d:\n", i+1)
		fmt.Printf("  Content=%d, cache.contentLen=%d, cache.valid=%v\n",
			len(w.Content), w.cache.contentLen, w.cache.valid)

		// In Render(), this check happens:
		// if len(w.Content) == w.cache.contentLen { ... } else { w.cache.valid = false }
		// Since Content changed, cache.valid becomes false
		// Then rebuildCache() is called, which calls renderGenericContent()

		_ = wb.GetTotalLinesVirtual()
		fmt.Printf("After GetTotalLinesVirtual %d:\n", i+1)
		fmt.Printf("  cache.valid=%v, cache.contentLen=%d\n", w.cache.valid, w.cache.contentLen)
	}
}

// BenchmarkSingleWindowStreaming tests streaming with just one window
func BenchmarkSingleWindowStreaming(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "initial content")
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate(streamID, "TA", " word")
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkSingleWindowStreamingDebug prints debug info
func BenchmarkSingleWindowStreamingDebug(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "initial content")
	_ = wb.GetTotalLinesVirtual()

	// Check initial state
	w := wb.Windows[0]
	fmt.Printf("Initial: wrappedLines=%d, content=%q, cache.valid=%v\n",
		len(w.cache.wrappedLines), w.Content, w.cache.valid)

	for i := 0; i < 3; i++ {
		wb.AppendOrUpdate(streamID, "TA", " word")
		fmt.Printf("After append %d: wrappedLines=%d, content=%q, cache.valid=%v\n",
			i+1, len(w.cache.wrappedLines), w.Content, w.cache.valid)

		// Check what's in wrappedLines
		if len(w.cache.wrappedLines) > 0 {
			fmt.Printf("  wrappedLines[0]=%q (len=%d)\n", w.cache.wrappedLines[0], len(w.cache.wrappedLines[0]))
		}

		_ = wb.GetTotalLinesVirtual()
		fmt.Printf("After ensureLineHeights %d: wrappedLines=%d, cache.valid=%v\n",
			i+1, len(w.cache.wrappedLines), w.cache.valid)
		if len(w.cache.wrappedLines) > 0 {
			fmt.Printf("  wrappedLines[0]=%q (len=%d)\n", w.cache.wrappedLines[0], len(w.cache.wrappedLines[0]))
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate(streamID, "TA", " word")
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkLongContentStreaming tests with longer content to trigger wrapping
func BenchmarkLongContentStreaming(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	streamID := "stream"
	// Start with content long enough to wrap
	wb.AppendOrUpdate(streamID, "TA", strings.Repeat("This is a line that will wrap. ", 10))
	_ = wb.GetTotalLinesVirtual()

	w := wb.Windows[0]
	fmt.Printf("Initial: wrappedLines=%d, contentLen=%d, styles=%v\n",
		len(w.cache.wrappedLines), len(w.Content), w.styles != nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wb.AppendOrUpdate(streamID, "TA", " more text")
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkDirectAppend tests AppendContent directly
func BenchmarkDirectAppend(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	w := &Window{
		ID:      "test",
		Tag:     "TA",
		Content: strings.Repeat("This is a line that will wrap. ", 10),
		Folded:  false,
		styles:  styles,
	}

	// Initial render to populate cache
	w.Render(80, false, styles,
		lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
		lipgloss.NewStyle())

	fmt.Printf("Initial: wrappedLines=%d, contentLen=%d, styles=%v\n",
		len(w.cache.wrappedLines), len(w.Content), w.styles != nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.AppendContent(" more", 76)
		_ = w.Render(80, false, styles,
			lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
			lipgloss.NewStyle())
	}
}

// BenchmarkDirectAppendNoStyles tests without styles
func BenchmarkDirectAppendNoStyles(b *testing.B) {
	w := &Window{
		ID:      "test",
		Tag:     "TA",
		Content: strings.Repeat("This is a line that will wrap. ", 10),
		Folded:  false,
		styles:  nil, // No styles!
	}

	styles := NewStyles(DefaultTheme())
	w.Render(80, false, styles,
		lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
		lipgloss.NewStyle())

	fmt.Printf("Initial (no styles): wrappedLines=%d\n", len(w.cache.wrappedLines))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.AppendContent(" more", 76)
		_ = w.Render(80, false, styles,
			lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
			lipgloss.NewStyle())
	}
}

// BenchmarkDirectAppendDebug shows what's happening
func BenchmarkDirectAppendDebug(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	w := &Window{
		ID:      "test",
		Tag:     "TA",
		Content: strings.Repeat("This is a line that will wrap. ", 10),
		Folded:  false,
		styles:  styles,
	}

	// Initial render
	w.Render(80, false, styles,
		lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
		lipgloss.NewStyle())

	fmt.Printf("Initial: wrappedLines=%d, cache.width=%d, width-4=%d\n",
		len(w.cache.wrappedLines), w.cache.width, w.cache.width-4)

	for i := 0; i < 3; i++ {
		w.AppendContent(" more text here", 76)
		fmt.Printf("After AppendContent %d: wrappedLines=%d, cache.valid=%v\n",
			i+1, len(w.cache.wrappedLines), w.cache.valid)

		// Check fast path condition
		fmt.Printf("  Fast path check: len(wrappedLines)=%d, cache.width-4=%d, innerWidth=76\n",
			len(w.cache.wrappedLines), w.cache.width-4)

		_ = w.Render(80, false, styles,
			lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
			lipgloss.NewStyle())
		fmt.Printf("After Render %d: wrappedLines=%d, cache.valid=%v\n",
			i+1, len(w.cache.wrappedLines), w.cache.valid)
	}
}

// BenchmarkRenderAfterAppend tests render after append (should use fast path)
func BenchmarkRenderAfterAppend(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	w := &Window{
		ID:      "test",
		Tag:     "TA",
		Content: strings.Repeat("This is a line that will wrap. ", 10),
		Folded:  false,
		styles:  styles,
	}

	// Initial render to populate cache
	w.Render(80, false, styles,
		lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
		lipgloss.NewStyle())

	fmt.Printf("Initial: wrappedLines=%d, cache.valid=%v, cache.width=%d\n",
		len(w.cache.wrappedLines), w.cache.valid, w.cache.width)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		w.AppendContent(" more text here", 76)
		b.StartTimer()

		_ = w.Render(80, false, styles,
			lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
			lipgloss.NewStyle())
	}
}

// BenchmarkFullRebuildAfterAppend tests full rebuild (when wrappedLines is nil)
func BenchmarkFullRebuildAfterAppend(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	w := &Window{
		ID:      "test",
		Tag:     "TA",
		Content: strings.Repeat("This is a line that will wrap. ", 10),
		Folded:  false,
		styles:  styles,
	}

	// Initial render
	w.Render(80, false, styles,
		lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
		lipgloss.NewStyle())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		w.AppendContent(" more text here", 76)
		w.cache.wrappedLines = nil // Force full rebuild
		b.StartTimer()

		_ = w.Render(80, false, styles,
			lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
			lipgloss.NewStyle())
	}
}

// BenchmarkStreamingUpdateWithVirtualRendering shows virtual rendering benefit
// This test shows virtual rendering helping when viewport is NOT at the bottom
func BenchmarkStreamingUpdateWithVirtualRendering(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows - user is viewing the middle
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport in the MIDDLE of content (virtual rendering should help here)
	wb.SetViewportPosition(150, 30) // Middle of ~300 lines
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta to last window (which is outside viewport)
		wb.AppendOrUpdate("msg99", "TA", " more")
		_ = wb.GetTotalLinesVirtual()
		_ = wb.GetAll(-1)
	}
}

// BenchmarkStreamingUpdateWithoutVirtualRendering shows cost without virtual rendering
func BenchmarkStreamingUpdateWithoutVirtualRendering(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows - user is viewing the middle
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// No viewport set - full render every time
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate delta to last window
		wb.AppendOrUpdate("msg99", "TA", " more")
		_ = wb.GetTotalLinesVirtual()
		_ = wb.GetAll(-1)
	}
}

// BenchmarkGetAllOnly isolates the GetAll cost
func BenchmarkGetAllWithVirtual(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport
	wb.SetViewportPosition(150, 30)
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1)
	}
}

func BenchmarkGetAllWithoutVirtual(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// No viewport
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetAll(-1)
	}
}

// ============================================================================
// Performance Summary
// ============================================================================
//
// Virtual Rendering Performance (100 windows, viewport showing middle 30 lines):
//
// GetAll (rendering only):
//   - With virtual rendering:    ~17μs (3.5x faster)
//   - Without virtual rendering: ~59μs
//
// Full update cycle (delta + GetAll):
//   - Incremental (1 dirty):     ~40μs
//   - Full rebuild (all dirty):  ~4ms (100x slower)
//
// Incremental wrapping:
//   - Incremental append:        ~1.5μs
//   - Full wrap:                 ~78μs (52x slower)
//
// Cursor movement (single):
//   - EnsureCursorVisible + updateContent: ~210μs
//
// Realistic streaming (profiled):
//   - Average render time:       ~500-600μs per update
//   - Render overhead:           ~1% of total time (at 50ms intervals)
//   - Updates/second:            ~4500
//
// Conclusion: NO RATE LIMITING NEEDED
//   - Data ingestion already throttled at 100ms (output.go)
//   - Render overhead is only 1% of wall time
//   - updateContent() skips unchanged content efficiently
//   - Virtual rendering provides 3.5x speedup
func BenchmarkWindowBufferResize(b *testing.B) {
	styles := NewStyles(DefaultTheme())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		wb := NewWindowBuffer(80, styles)

		// Create 50 windows
		for j := 0; j < 50; j++ {
			id := fmt.Sprintf("msg%d", j)
			content := strings.Repeat("Content line\n", 5)
			wb.AppendOrUpdate(id, "TA", content)
		}
		_ = wb.GetTotalLinesVirtual()

		b.StartTimer()

		// Simulate resize
		wb.SetWidth(120)
		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkVirtualRenderingScroll benchmarks scrolling with virtual rendering.
func BenchmarkVirtualRenderingScroll(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("This is a test message with some content.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	_ = wb.GetTotalLinesVirtual()

	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.updateContent()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Scroll through content
		for j := 0; j < 100; j++ {
			dm.ScrollDown(1)
			dm.updateContent()
		}
		for j := 0; j < 100; j++ {
			dm.ScrollUp(1)
			dm.updateContent()
		}
	}
}

// BenchmarkGetWindowStartLineCached benchmarks GetWindowStartLine when lineHeights are already cached.
func BenchmarkGetWindowStartLineCached(b *testing.B) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Content line\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Pre-calculate line heights
	_ = wb.GetTotalLinesVirtual()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wb.GetWindowStartLine(50)
		_ = wb.GetWindowStartLine(25)
		_ = wb.GetWindowStartLine(75)
	}
}

// BenchmarkEnsureLineHeightsIncremental vs full rebuild
func BenchmarkEnsureLineHeightsIncremental(b *testing.B) {
	styles := NewStyles(DefaultTheme())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create fresh buffer for each iteration
		wb := NewWindowBuffer(80, styles)
		for j := 0; j < 100; j++ {
			id := fmt.Sprintf("msg%d", j)
			content := strings.Repeat("Content line\n", 5)
			wb.AppendOrUpdate(id, "TA", content)
		}
		// Pre-calculate line heights
		_ = wb.GetTotalLinesVirtual()

		// Now append to one window (incremental path)
		wb.AppendOrUpdate("msg50", "TA", " new content")
		b.StartTimer()

		_ = wb.GetTotalLinesVirtual()
	}
}

func BenchmarkEnsureLineHeightsFullRebuild(b *testing.B) {
	styles := NewStyles(DefaultTheme())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create fresh buffer for each iteration
		wb := NewWindowBuffer(80, styles)
		for j := 0; j < 100; j++ {
			id := fmt.Sprintf("msg%d", j)
			content := strings.Repeat("Content line\n", 5)
			wb.AppendOrUpdate(id, "TA", content)
		}
		// Don't pre-calculate - force full rebuild
		b.StartTimer()

		_ = wb.GetTotalLinesVirtual()
	}
}

// BenchmarkIncrementalWrapping vs full wrapping
func BenchmarkIncrementalWrappingPath(b *testing.B) {
	styles := NewStyles(DefaultTheme())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		wb := NewWindowBuffer(80, styles)

		// Create one window with initial content
		wb.AppendOrUpdate("msg0", "TA", strings.Repeat("Initial content line\n", 10))
		// Trigger initial render to populate wrappedLines cache
		_ = wb.GetTotalLinesVirtual()

		b.StartTimer()

		// Append small delta - should use incremental wrapping
		wb.AppendOrUpdate("msg0", "TA", " new text")
		_ = wb.GetTotalLinesVirtual()
	}
}

func BenchmarkFullWrappingPath(b *testing.B) {
	styles := NewStyles(DefaultTheme())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		wb := NewWindowBuffer(80, styles)

		// Create one window with content
		wb.AppendOrUpdate("msg0", "TA", strings.Repeat("Initial content line\n", 10))

		b.StartTimer()

		// First render - full wrapping
		_ = wb.GetTotalLinesVirtual()
	}
}
