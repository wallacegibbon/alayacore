package terminal

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestQueueManagerSetItems(t *testing.T) {
	styles := DefaultStyles()
	qm := NewQueueManager(styles)

	// Initially empty
	if len(qm.items) != 0 {
		t.Errorf("Expected 0 items initially, got %d", len(qm.items))
	}

	// Set some items
	items := []QueueItem{
		{QueueID: "Q1", Type: "prompt", Content: "test 1", CreatedAt: time.Now()},
		{QueueID: "Q2", Type: "command", Content: "test 2", CreatedAt: time.Now()},
		{QueueID: "Q3", Type: "prompt", Content: "test 3", CreatedAt: time.Now()},
	}

	qm.SetItems(items)

	if len(qm.items) != 3 {
		t.Errorf("Expected 3 items after SetItems, got %d", len(qm.items))
	}

	// Verify items are copied
	if qm.items[0].QueueID != "Q1" {
		t.Errorf("Expected first item ID to be Q1, got %s", qm.items[0].QueueID)
	}
}

func TestQueueManagerNavigation(t *testing.T) {
	styles := DefaultStyles()
	qm := NewQueueManager(styles)
	qm.Open()

	// Set 3 items
	items := []QueueItem{
		{QueueID: "Q1", Type: "prompt", Content: "test 1", CreatedAt: time.Now()},
		{QueueID: "Q2", Type: "command", Content: "test 2", CreatedAt: time.Now()},
		{QueueID: "Q3", Type: "prompt", Content: "test 3", CreatedAt: time.Now()},
	}
	qm.SetItems(items)

	// Initially selected first item
	if qm.selectedIdx != 0 {
		t.Errorf("Expected selectedIdx to be 0, got %d", qm.selectedIdx)
	}

	// Move down - simulate key handling
	if len(qm.items) > 0 && qm.selectedIdx < len(qm.items)-1 {
		qm.selectedIdx++
	}
	if qm.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1 after j, got %d", qm.selectedIdx)
	}

	// Move down again
	if len(qm.items) > 0 && qm.selectedIdx < len(qm.items)-1 {
		qm.selectedIdx++
	}
	if qm.selectedIdx != 2 {
		t.Errorf("Expected selectedIdx to be 2 after second j, got %d", qm.selectedIdx)
	}

	// Try to move past end - should stay at 2
	if len(qm.items) > 0 && qm.selectedIdx < len(qm.items)-1 {
		qm.selectedIdx++
	}
	if qm.selectedIdx != 2 {
		t.Errorf("Expected selectedIdx to stay at 2, got %d", qm.selectedIdx)
	}

	// Move up
	if qm.selectedIdx > 0 {
		qm.selectedIdx--
	}
	if qm.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be 1 after k, got %d", qm.selectedIdx)
	}
}

func TestQueueManagerGetSelectedItem(t *testing.T) {
	styles := DefaultStyles()
	qm := NewQueueManager(styles)
	qm.Open()

	// Empty queue - should return nil
	item := qm.GetSelectedItem()
	if item != nil {
		t.Error("Expected nil for empty queue")
	}

	// Set items
	items := []QueueItem{
		{QueueID: "Q1", Type: "prompt", Content: "test 1", CreatedAt: time.Now()},
		{QueueID: "Q2", Type: "command", Content: "test 2", CreatedAt: time.Now()},
	}
	qm.SetItems(items)

	// Get first item
	item = qm.GetSelectedItem()
	if item == nil {
		t.Fatal("Expected non-nil item")
		return
	}
	if item.QueueID != "Q1" {
		t.Errorf("Expected Q1, got %s", item.QueueID)
	}

	// Move to second item
	qm.selectedIdx = 1
	item = qm.GetSelectedItem()
	if item == nil {
		t.Fatal("Expected non-nil item")
		return
	}
	if item.QueueID != "Q2" {
		t.Errorf("Expected Q2, got %s", item.QueueID)
	}
}

func TestQueueManagerOpenClose(t *testing.T) {
	styles := DefaultStyles()
	qm := NewQueueManager(styles)

	if qm.IsOpen() {
		t.Error("Queue manager should not be open initially")
	}

	qm.Open()
	if !qm.IsOpen() {
		t.Error("Queue manager should be open after Open()")
	}

	qm.Close()
	if qm.IsOpen() {
		t.Error("Queue manager should not be open after Close()")
	}
}

func TestQueueManagerDeleteLastItem(t *testing.T) {
	styles := DefaultStyles()
	qm := NewQueueManager(styles)
	qm.Open()

	// Set 3 items
	items := []QueueItem{
		{QueueID: "Q1", Type: "prompt", Content: "test 1", CreatedAt: time.Now()},
		{QueueID: "Q2", Type: "command", Content: "test 2", CreatedAt: time.Now()},
		{QueueID: "Q3", Type: "prompt", Content: "test 3", CreatedAt: time.Now()},
	}
	qm.SetItems(items)

	// Move to last item
	qm.selectedIdx = 2
	qm.scrollIdx = 0

	// Simulate deleting the last item - set items to 2 items
	newItems := []QueueItem{
		{QueueID: "Q1", Type: "prompt", Content: "test 1", CreatedAt: time.Now()},
		{QueueID: "Q2", Type: "command", Content: "test 2", CreatedAt: time.Now()},
	}
	qm.SetItems(newItems)

	// selectedIdx should be clamped to 1 (last valid index)
	if qm.selectedIdx != 1 {
		t.Errorf("Expected selectedIdx to be clamped to 1, got %d", qm.selectedIdx)
	}

	// GetSelectedItem should return Q2
	item := qm.GetSelectedItem()
	if item == nil {
		t.Fatal("Expected non-nil item")
		return
	}
	if item.QueueID != "Q2" {
		t.Errorf("Expected Q2, got %s", item.QueueID)
	}

	// Test deleting all items
	emptyItems := []QueueItem{}
	qm.SetItems(emptyItems)

	// selectedIdx should be 0
	if qm.selectedIdx != 0 {
		t.Errorf("Expected selectedIdx to be 0 for empty list, got %d", qm.selectedIdx)
	}

	// scrollIdx should also be 0
	if qm.scrollIdx != 0 {
		t.Errorf("Expected scrollIdx to be 0 for empty list, got %d", qm.scrollIdx)
	}

	// GetSelectedItem should return nil for empty list
	item = qm.GetSelectedItem()
	if item != nil {
		t.Error("Expected nil for empty queue")
	}
}

func TestQueueManagerRenderItemTruncation(t *testing.T) {
	styles := DefaultStyles()
	qm := NewQueueManager(styles)
	qm.width = 60
	maxWidth := qm.width - 6 // 54 display cells

	// ASCII content that fits — should not be truncated
	short := QueueItem{QueueID: "Q1", Type: "prompt", Content: "hello world"}
	rendered := qm.renderItem(short, false)
	if strings.Contains(rendered, "...") {
		t.Errorf("short ASCII content should not be truncated, got: %s", rendered)
	}

	// ASCII content that exceeds maxWidth — should be truncated
	longASCII := QueueItem{QueueID: "Q2", Type: "prompt", Content: strings.Repeat("a", maxWidth+10)}
	rendered = qm.renderItem(longASCII, false)
	if !strings.Contains(rendered, "...") {
		t.Errorf("long ASCII content should be truncated with ...")
	}

	// CJK content that fits display width (27 chars = 54 cells = maxWidth)
	cjkFit := QueueItem{QueueID: "Q3", Type: "prompt", Content: strings.Repeat("日", maxWidth/2)}
	rendered = qm.renderItem(cjkFit, false)
	if strings.Contains(rendered, "...") {
		t.Errorf("CJK content fitting maxWidth should not be truncated, got: %s", rendered)
	}

	// CJK content exceeding display width (28 chars = 56 cells > 54)
	cjkOver := QueueItem{QueueID: "Q4", Type: "prompt", Content: strings.Repeat("日", maxWidth/2+1)}
	rendered = qm.renderItem(cjkOver, false)
	if !strings.Contains(rendered, "...") {
		t.Errorf("CJK content exceeding maxWidth should be truncated with ..., got: %s", rendered)
	}

	// Verify rendered output is valid UTF-8
	if !utf8.ValidString(rendered) {
		t.Errorf("rendered content contains invalid UTF-8: %q", rendered)
	}

	// Mixed ASCII + CJK: "ab" (2 cells) + 26 CJK (52 cells) = 54 = maxWidth, should fit
	mixedFit := QueueItem{QueueID: "Q5", Type: "prompt", Content: "ab" + strings.Repeat("日", 26)}
	rendered = qm.renderItem(mixedFit, false)
	if strings.Contains(rendered, "...") {
		t.Errorf("mixed content fitting maxWidth should not be truncated, got: %s", rendered)
	}

	// Mixed: "ab" (2 cells) + 27 CJK (54 cells) = 56 > 54, should truncate
	mixedOver := QueueItem{QueueID: "Q6", Type: "prompt", Content: "ab" + strings.Repeat("日", 27)}
	rendered = qm.renderItem(mixedOver, false)
	if !strings.Contains(rendered, "...") {
		t.Errorf("mixed content exceeding maxWidth should be truncated with ..., got: %s", rendered)
	}
}
