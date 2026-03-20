package terminal

import (
	"fmt"
	"strings"
	"testing"
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
