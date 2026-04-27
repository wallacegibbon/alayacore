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

	// Scroll to the very bottom so window-1 is entirely above the viewport
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

// TestEnsureCursorVisible_NormalWindowStillScrolls verifies that the fix
// doesn't break the normal case where a regular-sized window needs scrolling.
func TestEnsureCursorVisible_NormalWindowStillScrolls(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())
	display := NewDisplayModel(wb, DefaultStyles())

	// 20 windows, each 5 lines → 100 total lines
	for i := range 20 {
		wb.AppendOrUpdate("window-"+strings.Repeat("x", i+1), stream.TagTextAssistant,
			strings.Repeat("line\n", 5))
	}

	display.SetHeight(20) // viewport shows 20 lines
	display.updateContent()

	// Cursor on last window (lines 95-100)
	display.SetWindowCursor(19)

	// Start at top of content
	display.viewport.SetYOffset(0)

	display.EnsureCursorVisible()

	// Should have scrolled down to show the window
	yOffset := display.viewport.YOffset()
	endLine := wb.GetWindowEndLine(19)
	startLine := wb.GetWindowStartLine(19)
	vpTop := yOffset
	vpBottom := vpTop + 20

	if startLine < vpTop {
		t.Errorf("window start above viewport: startLine=%d, viewportTop=%d", startLine, vpTop)
	}
	if endLine > vpBottom {
		t.Errorf("window end below viewport: endLine=%d, viewportBottom=%d", endLine, vpBottom)
	}
}
