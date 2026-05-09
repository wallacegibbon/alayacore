package terminal

import (
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

// TestEnsureCursorVisible_OversizedWindowDoesNotOscillate verifies that
// repeatedly calling EnsureCursorVisible on a window taller than the
// viewport does not cause the YOffset to oscillate between two positions.
func TestEnsureCursorVisible_OversizedWindowDoesNotOscillate(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Window 0: small (1 line)
	// Window 1: oversized — 100 lines, much taller than the viewport
	wb.AppendOrUpdate("window-1", stream.TagTextAssistant, "Small window")
	wb.AppendOrUpdate("window-2", stream.TagTextAssistant, strings.Repeat("line\n", 100))

	// Set viewport height to 50 (smaller than window-2 which is ~100 lines)
	display.SetHeight(50)
	display.updateContent()

	// Place cursor on the oversized last window
	display.SetWindowCursor(1)

	// Simulate pressing "L" (MoveWindowCursorToBottom + EnsureCursorVisible + updateContent)
	// The first press scrolls to make the window visible
	display.MoveWindowCursorToBottom()
	display.EnsureCursorVisible()
	display.updateContent()

	yOffsetAfterFirst := display.viewport.YOffset()
	t.Logf("YOffset after 1st L: %d", yOffsetAfterFirst)

	// Press "L" a second time — should NOT change the YOffset
	display.MoveWindowCursorToBottom()
	display.EnsureCursorVisible()
	display.updateContent()

	yOffsetAfterSecond := display.viewport.YOffset()
	t.Logf("YOffset after 2nd L: %d", yOffsetAfterSecond)

	// Press "L" a third time — must stay stable
	display.MoveWindowCursorToBottom()
	display.EnsureCursorVisible()
	display.updateContent()

	yOffsetAfterThird := display.viewport.YOffset()
	t.Logf("YOffset after 3rd L: %d", yOffsetAfterThird)

	if yOffsetAfterSecond != yOffsetAfterThird {
		t.Errorf("YOffset oscillated: 2nd=%d, 3rd=%d (should be stable)",
			yOffsetAfterSecond, yOffsetAfterThird)
	}
}

// TestEnsureCursorVisible_OversizedWindowScrollsWhenOffScreen verifies that
// EnsureCursorVisible still scrolls when an oversized window is completely
// off-screen (entirely above or entirely below the viewport).
func TestEnsureCursorVisible_OversizedWindowScrollsWhenOffScreen(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// Window 0: oversized — 100 lines
	// Window 1: small tail window
	wb.AppendOrUpdate("window-1", stream.TagTextAssistant, strings.Repeat("line\n", 100))
	wb.AppendOrUpdate("window-2", stream.TagTextAssistant, "Tail")

	display.SetHeight(50)
	display.updateContent()

	display.SetWindowCursor(0)

	// Scroll to the very bottom so window-0 is entirely above the viewport
	display.viewport.GotoBottom()
	display.updateContent()

	yOffsetBefore := display.viewport.YOffset()
	t.Logf("YOffset before (at bottom): %d", yOffsetBefore)

	// Now ensure the oversized window is visible — it's entirely above,
	// so it should scroll to show its bottom edge
	display.EnsureCursorVisible()

	yOffsetAfter := display.viewport.YOffset()
	t.Logf("YOffset after EnsureCursorVisible: %d", yOffsetAfter)

	startLine := wb.GetWindowStartLine(0)
	endLine := wb.GetWindowEndLine(0)
	vpTop := yOffsetAfter
	vpBottom := vpTop + 50

	// The window should now be at least partially visible
	if endLine <= vpTop {
		t.Errorf("window still entirely above viewport: endLine=%d, viewportTop=%d", endLine, vpTop)
	}
	if startLine >= vpBottom {
		t.Errorf("window still entirely below viewport: startLine=%d, viewportBottom=%d", startLine, vpBottom)
	}
}

// TestEnsureCursorVisible_PartiallyVisibleWindowNotMoved verifies that a
// normal-sized window that is already partially visible is NOT scrolled.
func TestEnsureCursorVisible_PartiallyVisibleWindowNotMoved(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// 20 windows, each ~8 rendered lines → ~160 total lines
	for i := range 20 {
		wb.AppendOrUpdate("window-"+strings.Repeat("x", i+1), stream.TagTextAssistant,
			strings.Repeat("line\n", 5))
	}

	display.SetHeight(20) // viewport shows 20 lines
	display.updateContent()

	// Use window index 10 — NOT the last window, so autoFollow won't
	// force the viewport to the bottom.
	display.SetWindowCursor(10) // disables autoFollow

	startLine := wb.GetWindowStartLine(10)
	endLine := wb.GetWindowEndLine(10)
	t.Logf("Window 10: lines %d-%d", startLine, endLine)

	// Position viewport so the window is partially visible
	partialOffset := startLine + 2
	display.viewport.SetYOffset(partialOffset)

	yOffsetBefore := display.viewport.YOffset()
	t.Logf("YOffset before: %d (viewport %d-%d)", yOffsetBefore, yOffsetBefore, yOffsetBefore+20)

	display.EnsureCursorVisible()

	yOffsetAfter := display.viewport.YOffset()
	t.Logf("YOffset after: %d", yOffsetAfter)

	if yOffsetAfter != yOffsetBefore {
		t.Errorf("YOffset changed from %d to %d for a partially visible window (should stay)",
			yOffsetBefore, yOffsetAfter)
	}
}

// TestEnsureCursorVisible_OffScreenWindowScrolls verifies that a window
// completely off-screen (below viewport) does get scrolled into view.
func TestEnsureCursorVisible_OffScreenWindowScrolls(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	for i := range 20 {
		wb.AppendOrUpdate("window-"+strings.Repeat("x", i+1), stream.TagTextAssistant,
			strings.Repeat("line\n", 5))
	}

	display.SetHeight(20)
	display.updateContent()

	// Use window index 10 — NOT the last window to avoid autoFollow
	display.SetWindowCursor(10)

	// Start at top — window 10 is entirely below viewport
	display.viewport.SetYOffset(0)

	yOffsetBefore := display.viewport.YOffset()
	t.Logf("YOffset before: %d", yOffsetBefore)

	display.EnsureCursorVisible()

	yOffsetAfter := display.viewport.YOffset()
	t.Logf("YOffset after: %d", yOffsetAfter)

	if yOffsetAfter == yOffsetBefore {
		t.Errorf("YOffset unchanged at %d for off-screen window (should scroll)", yOffsetBefore)
	}

	startLine := wb.GetWindowStartLine(10)
	vpTop := yOffsetAfter
	vpBottom := vpTop + 20

	if startLine >= vpBottom {
		t.Errorf("window still entirely below viewport: startLine=%d, viewportBottom=%d", startLine, vpBottom)
	}
}

// TestEnsureCursorVisible_EntirelyAboveScrolls verifies that a window
// completely above the viewport gets scrolled into view.
func TestEnsureCursorVisible_EntirelyAboveScrolls(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	for i := range 20 {
		wb.AppendOrUpdate("window-"+strings.Repeat("x", i+1), stream.TagTextAssistant,
			strings.Repeat("line\n", 5))
	}

	display.SetHeight(20)
	display.updateContent()

	// Use window index 3 — NOT the last window
	display.SetWindowCursor(3)

	// Scroll to bottom so the window is entirely above the viewport
	display.viewport.GotoBottom()
	display.updateContent()

	display.EnsureCursorVisible()

	endLine := wb.GetWindowEndLine(3)
	vpTop := display.viewport.YOffset()

	if endLine <= vpTop {
		t.Errorf("window still entirely above viewport: endLine=%d, viewportTop=%d", endLine, vpTop)
	}
}

// TestAutoFollow_OnlyGEnables verifies the new auto-follow design:
//   - Only G enables auto-follow
//   - All other navigation disables it
//   - auto-follow keeps viewport at bottom across updateContent calls
func TestAutoFollow_OnlyGEnables(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate("w1", stream.TagTextAssistant, strings.Repeat("line\n", 5))
	wb.AppendOrUpdate("w2", stream.TagTextAssistant, strings.Repeat("line\n", 5))
	wb.AppendOrUpdate("w3", stream.TagTextAssistant, strings.Repeat("line\n", 5))

	display.SetHeight(10)
	display.updateContent()

	// Start: autoFollow is true by default (SetCursorToLastWindow from init)
	// Simulate G: enables auto-follow
	display.SetCursorToLastWindow()
	display.GotoBottom()
	display.updateContent()

	if !display.autoFollow {
		t.Fatal("expected autoFollow=true after G")
	}
	yOffsetAtBottom := display.viewport.YOffset()
	t.Logf("After G: YOffset=%d, autoFollow=%v", yOffsetAtBottom, display.autoFollow)

	// Now navigate up with k — disables auto-follow
	display.MoveWindowCursorUp()
	display.EnsureCursorVisible()
	display.updateContent()

	if display.autoFollow {
		t.Fatal("expected autoFollow=false after k")
	}

	// Simulate new content arriving — viewport should NOT jump to bottom
	wb.AppendOrUpdate("w3", stream.TagTextAssistant, strings.Repeat("line\n", 20))
	display.updateContent()

	if display.viewport.YOffset() != yOffsetAtBottom {
		t.Errorf("viewport moved after new content while autoFollow=false (YOffset changed from %d to %d)",
			yOffsetAtBottom, display.viewport.YOffset())
	}

	// Now press G again — re-enables auto-follow
	display.SetCursorToLastWindow()
	display.GotoBottom()
	display.updateContent()

	if !display.autoFollow {
		t.Fatal("expected autoFollow=true after second G")
	}
	yOffsetNow := display.viewport.YOffset()
	t.Logf("After second G: YOffset=%d", yOffsetNow)

	// More new content — should follow to bottom
	wb.AppendOrUpdate("w3", stream.TagTextAssistant, strings.Repeat("line\n", 50))
	display.updateContent()

	yOffsetFinal := display.viewport.YOffset()
	t.Logf("After new content (following): YOffset=%d", yOffsetFinal)

	totalLines := wb.GetTotalLines()
	expectedOffset := max(0, totalLines-display.GetHeight())
	if yOffsetFinal != expectedOffset {
		t.Errorf("expected YOffset=%d (bottom), got %d", expectedOffset, yOffsetFinal)
	}
}

// TestAutoFollow_JNoOpWhenNewWindowBetweenTicks is a regression test for a
// race where a new window is appended to the buffer between key events and
// the periodic tick.  Before the fix, pressing j while auto-following would
// silently move the cursor to the (invisible) new window and disable
// auto-follow, because MoveWindowCursorDown checked windowCount at the
// moment of the keypress rather than honoring the auto-follow invariant.
//
// Expected behavior: when auto-follow is active, j (MoveWindowCursorDown)
// and L (MoveWindowCursorToBottom) are always no-ops.
func TestAutoFollow_JNoOpWhenNewWindowBetweenTicks(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	wb.AppendOrUpdate("w1", stream.TagTextAssistant, strings.Repeat("line\n", 5))
	wb.AppendOrUpdate("w2", stream.TagTextAssistant, strings.Repeat("line\n", 5))
	wb.AppendOrUpdate("w3", stream.TagTextAssistant, strings.Repeat("line\n", 5))

	display.SetHeight(20)
	display.updateContent()

	// Simulate G: cursor at last window, auto-follow enabled.
	display.SetCursorToLastWindow()
	display.GotoBottom()
	display.updateContent()

	if !display.autoFollow {
		t.Fatal("expected autoFollow=true after G")
	}
	cursorBefore := display.GetWindowCursor()
	t.Logf("After G: cursor=%d, autoFollow=%v", cursorBefore, display.autoFollow)

	// Press j while at the last window — should be a no-op.
	moved := display.MoveWindowCursorDown()
	if moved {
		t.Fatal("j at last window with autoFollow should be a no-op")
	}
	if !display.autoFollow {
		t.Fatal("autoFollow should still be true after j at last window")
	}

	// Now a new window arrives between ticks (buffer updated, tick not yet fired).
	wb.AppendOrUpdate("w4", stream.TagTextAssistant, strings.Repeat("line\n", 5))
	// Window count is now 4; cursor is still at old last (index 2).
	// Before the fix, pressing j here would move cursor to index 3 and
	// disable auto-follow.

	moved = display.MoveWindowCursorDown()
	if moved {
		t.Errorf("j with autoFollow should be no-op even after new window; cursor=%d, windowCount=%d",
			display.GetWindowCursor(), wb.GetWindowCount())
	}
	if !display.autoFollow {
		t.Error("autoFollow should NOT be disabled by j when a new window was appended between ticks")
	}
	if display.GetWindowCursor() != cursorBefore {
		t.Errorf("cursor should stay at %d, got %d", cursorBefore, display.GetWindowCursor())
	}

	// L (MoveWindowCursorToBottom) should also be a no-op while auto-following.
	moved = display.MoveWindowCursorToBottom()
	if moved {
		t.Errorf("L with autoFollow should be no-op; cursor=%d", display.GetWindowCursor())
	}
	if !display.autoFollow {
		t.Error("autoFollow should NOT be disabled by L when auto-following")
	}

	// Now simulate the tick handler: SetCursorToLastWindow advances cursor
	// to the new last window and keeps auto-follow.
	display.SetCursorToLastWindow()
	display.GotoBottom()
	display.updateContent()

	if display.GetWindowCursor() != 3 {
		t.Errorf("after tick, cursor should be at last window (3), got %d", display.GetWindowCursor())
	}
	if !display.autoFollow {
		t.Error("autoFollow should still be true after tick")
	}
}
