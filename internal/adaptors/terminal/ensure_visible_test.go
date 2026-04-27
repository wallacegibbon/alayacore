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

	// Use window index 10 — NOT the last window, so shouldFollow() won't
	// force the viewport to the bottom.
	display.SetWindowCursor(10) // sets userMovedCursorAway = true

	startLine := wb.GetWindowStartLine(10)
	endLine := wb.GetWindowEndLine(10)
	t.Logf("Window 10: lines %d-%d", startLine, endLine)

	// Position viewport so the window is partially visible:
	// e.g., if window is lines 80-88, set YOffset=82 so viewport shows 82-102
	// Lines 82-88 are visible, lines 80-81 are above — partially visible.
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

	// Use window index 10 — NOT the last window to avoid shouldFollow()
	display.SetWindowCursor(10)

	// Start at top — window 10 is entirely below viewport (0-20 vs ~80-88)
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
