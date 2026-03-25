package terminal

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestStreamingProfile profiles realistic streaming behavior
// Run with: go test -run TestStreamingProfile -v
func TestStreamingProfile(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 windows (conversation history)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	// Set up viewport
	wb.SetViewportPosition(0, 30)
	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)

	// Create streaming window
	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "")
	dm.updateContent()

	// Profile streaming
	totalUpdates := 0
	totalTime := time.Duration(0)
	contentChanges := 0
	lastContent := ""

	// Simulate 100 streaming updates (typical short response)
	for i := 0; i < 100; i++ {
		start := time.Now()

		// Simulate delta
		wb.AppendOrUpdate(streamID, "TA", fmt.Sprintf(" word%d", i))

		// This is what handleTick does
		dm.updateContent()

		elapsed := time.Since(start)
		totalTime += elapsed
		totalUpdates++

		currentContent := dm.lastContent
		if currentContent != lastContent {
			contentChanges++
			lastContent = currentContent
		}
	}

	avgTime := totalTime / time.Duration(totalUpdates)

	t.Logf("\n=== Streaming Profile Results ===")
	t.Logf("Total updates: %d", totalUpdates)
	t.Logf("Content changes: %d", contentChanges)
	t.Logf("Total time: %v", totalTime)
	t.Logf("Average per update: %v", avgTime)
	t.Logf("Updates per second: %.1f", float64(totalUpdates)/totalTime.Seconds())
	t.Logf("Effective content change rate: %.1f/s", float64(contentChanges)/totalTime.Seconds())

	// Recommendations based on results
	if avgTime > 5*time.Millisecond {
		t.Logf("\n⚠️  SLOW: Average update time > 5ms - rate limiting recommended")
	} else if avgTime > 1*time.Millisecond {
		t.Logf("\n⚡ MODERATE: Average update time 1-5ms - rate limiting may help")
	} else {
		t.Logf("\n✅ FAST: Average update time < 1ms - no rate limiting needed")
	}
}

// TestStreamingProfileLongContent profiles with longer content
func TestStreamingProfileLongContent(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 20 windows
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Historical message content.\n", 3)
		wb.AppendOrUpdate(id, "TA", content)
	}

	wb.SetViewportPosition(0, 30)
	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)

	// Start with long content
	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", strings.Repeat("This is a line that will wrap. ", 20))
	dm.updateContent()

	// Profile 50 updates with growing content
	totalTime := time.Duration(0)
	times := []time.Duration{}

	for i := 0; i < 50; i++ {
		start := time.Now()
		wb.AppendOrUpdate(streamID, "TA", " more streaming text here")
		dm.updateContent()
		elapsed := time.Since(start)
		totalTime += elapsed
		times = append(times, elapsed)
	}

	avgTime := totalTime / 50
	minTime := times[0]
	maxTime := times[0]
	for _, d := range times {
		if d < minTime {
			minTime = d
		}
		if d > maxTime {
			maxTime = d
		}
	}

	t.Logf("\n=== Long Content Streaming Profile ===")
	t.Logf("Average: %v", avgTime)
	t.Logf("Min: %v", minTime)
	t.Logf("Max: %v", maxTime)
	t.Logf("Total for 50 updates: %v", totalTime)
}

// TestCursorMovementProfile profiles cursor movement performance
func TestCursorMovementProfile(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Create 100 windows
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("msg%d", i)
		content := strings.Repeat("Window content line.\n", 5)
		wb.AppendOrUpdate(id, "TA", content)
	}

	wb.SetViewportPosition(0, 30)
	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)
	dm.updateContent()

	// Profile cursor movement through all windows
	totalTime := time.Duration(0)
	times := []time.Duration{}

	for i := 0; i < 100; i++ {
		start := time.Now()
		dm.SetWindowCursor(i)
		dm.EnsureCursorVisible()
		dm.updateContent()
		elapsed := time.Since(start)
		totalTime += elapsed
		times = append(times, elapsed)
	}

	avgTime := totalTime / 100
	minTime := times[0]
	maxTime := times[0]
	for _, d := range times {
		if d < minTime {
			minTime = d
		}
		if d > maxTime {
			maxTime = d
		}
	}

	t.Logf("\n=== Cursor Movement Profile ===")
	t.Logf("Average: %v", avgTime)
	t.Logf("Min: %v", minTime)
	t.Logf("Max: %v", maxTime)
	t.Logf("Total for 100 moves: %v", totalTime)
	t.Logf("Moves per second: %.1f", 100/totalTime.Seconds())

	if avgTime > 500*time.Microsecond {
		t.Logf("\n⚠️  SLOW: Cursor movement > 500μs")
	} else {
		t.Logf("\n✅ FAST: Cursor movement < 500μs")
	}
}

// TestUpdateContentSkipRate measures how often updateContent skips due to unchanged content
func TestUpdateContentSkipRate(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	wb.AppendOrUpdate("msg0", "TA", "Content")
	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.updateContent()

	skipped := 0
	total := 100

	// Call updateContent without changing anything
	for i := 0; i < total; i++ {
		before := dm.lastContent
		dm.updateContent()
		if dm.lastContent == before {
			skipped++
		}
	}

	t.Logf("\n=== Update Content Skip Rate ===")
	t.Logf("Total calls: %d", total)
	t.Logf("Skipped (unchanged): %d (%.1f%%)", skipped, float64(skipped)/float64(total)*100)

	if skipped == total {
		t.Logf("✅ All redundant updates skipped")
	} else {
		t.Logf("⚠️  Some updates not skipped - check caching")
	}
}

// TestRealisticStreamingWithTiming simulates actual streaming with timing
func TestRealisticStreamingWithTiming(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Conversation history
	for i := 0; i < 10; i++ {
		wb.AppendOrUpdate(fmt.Sprintf("msg%d", i), "TA", "Previous message content.\n")
	}

	wb.SetViewportPosition(0, 30)
	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "")
	dm.updateContent()

	// Simulate realistic word-by-word streaming at 50ms intervals
	// (which is faster than actual LLM output)
	words := strings.Split("This is a simulated streaming response from the AI assistant. It contains multiple words that arrive incrementally.", " ")

	totalRenderTime := time.Duration(0)
	renderCount := 0

	for i, word := range words {
		// Simulate data arrival (not measured)
		wb.AppendOrUpdate(streamID, "TA", " "+word)

		// Measure render time
		start := time.Now()
		dm.updateContent()
		renderTime := time.Since(start)

		totalRenderTime += renderTime
		renderCount++

		// Simulate 50ms between word arrivals
		if i < len(words)-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}

	avgRenderTime := totalRenderTime / time.Duration(renderCount)
	totalWallTime := time.Duration(len(words)) * 50 * time.Millisecond

	t.Logf("\n=== Realistic Streaming Profile ===")
	t.Logf("Words streamed: %d", len(words))
	t.Logf("Simulated interval: 50ms")
	t.Logf("Total wall time: %v", totalWallTime)
	t.Logf("Total render time: %v", totalRenderTime)
	t.Logf("Average render time: %v", avgRenderTime)
	t.Logf("Render overhead: %.2f%%", float64(totalRenderTime)/float64(totalWallTime)*100)

	if totalRenderTime < totalWallTime/10 {
		t.Logf("✅ Render overhead < 10%% - no rate limiting needed")
	} else if totalRenderTime < totalWallTime/5 {
		t.Logf("⚡ Render overhead 10-20%% - acceptable")
	} else {
		t.Logf("⚠️  Render overhead > 20%% - consider rate limiting")
	}
}

// TestVeryLongContentStreaming profiles streaming with very long content
func TestVeryLongContentStreaming(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Large conversation history
	for i := 0; i < 50; i++ {
		wb.AppendOrUpdate(fmt.Sprintf("msg%d", i), "TA", strings.Repeat("Previous message content.\n", 5))
	}

	wb.SetViewportPosition(0, 30)
	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)

	streamID := "stream"
	// Start with already long content
	wb.AppendOrUpdate(streamID, "TA", strings.Repeat("Initial content line that is reasonably long. ", 20))
	dm.updateContent()

	// Simulate streaming more content
	totalRenderTime := time.Duration(0)
	times := []time.Duration{}

	for i := 0; i < 50; i++ {
		wb.AppendOrUpdate(streamID, "TA", fmt.Sprintf(" Additional sentence number %d here.", i))

		start := time.Now()
		dm.updateContent()
		elapsed := time.Since(start)

		totalRenderTime += elapsed
		times = append(times, elapsed)
	}

	avgTime := totalRenderTime / 50
	minTime := times[0]
	maxTime := times[0]
	for _, d := range times {
		if d < minTime {
			minTime = d
		}
		if d > maxTime {
			maxTime = d
		}
	}

	t.Logf("\n=== Very Long Content Streaming Profile ===")
	t.Logf("Windows: 51 (50 history + 1 streaming)")
	t.Logf("Streaming window content: ~%d chars", len(wb.Windows[50].Content))
	t.Logf("Average render time: %v", avgTime)
	t.Logf("Min render time: %v", minTime)
	t.Logf("Max render time: %v", maxTime)
	t.Logf("Total render time (50 updates): %v", totalRenderTime)

	// Check if performance degrades significantly with content length
	if maxTime > 2*avgTime {
		t.Logf("⚠️  Max time is 2x average - some updates are slow")
	}
	if avgTime < 1*time.Millisecond {
		t.Logf("✅ Average < 1ms - acceptable for long content")
	}
}

// TestWorstCaseStreaming tests extremely fast streaming (faster than real LLM)
func TestWorstCaseStreaming(t *testing.T) {
	styles := NewStyles(DefaultTheme())
	wb := NewWindowBuffer(80, styles)

	// Conversation history
	for i := 0; i < 10; i++ {
		wb.AppendOrUpdate(fmt.Sprintf("msg%d", i), "TA", "Previous message content.\n")
	}

	wb.SetViewportPosition(0, 30)
	dm := NewDisplayModel(wb, styles)
	dm.SetHeight(30)
	dm.SetWidth(80)
	dm.SetDisplayFocused(true)

	streamID := "stream"
	wb.AppendOrUpdate(streamID, "TA", "")
	dm.updateContent()

	// Worst case: 1000 updates as fast as possible
	const updates = 1000
	totalRenderTime := time.Duration(0)
	contentChanges := 0
	lastContent := ""

	start := time.Now()
	for i := 0; i < updates; i++ {
		renderStart := time.Now()
		wb.AppendOrUpdate(streamID, "TA", fmt.Sprintf(" w%d", i))
		dm.updateContent()
		renderTime := time.Since(renderStart)
		totalRenderTime += renderTime

		if dm.lastContent != lastContent {
			contentChanges++
			lastContent = dm.lastContent
		}
	}
	totalWallTime := time.Since(start)

	t.Logf("\n=== Worst Case Streaming (No Throttle) ===")
	t.Logf("Updates: %d", updates)
	t.Logf("Total wall time: %v", totalWallTime)
	t.Logf("Total render time: %v", totalRenderTime)
	t.Logf("Average render time: %v", totalRenderTime/time.Duration(updates))
	t.Logf("Content changes: %d", contentChanges)
	t.Logf("Updates/sec: %.1f", float64(updates)/totalWallTime.Seconds())
	t.Logf("Render overhead: %.1f%%", float64(totalRenderTime)/float64(totalWallTime)*100)

	// Check if we're CPU-bound
	if totalRenderTime > totalWallTime/2 {
		t.Logf("⚠️  Render-bound: render time > 50%% of wall time")
	} else {
		t.Logf("✅ Not render-bound: render time < 50%% of wall time")
	}
}
