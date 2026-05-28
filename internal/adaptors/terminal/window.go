package terminal

// Window is a single display unit with border and content.
// Caching is handled internally — callers just call Render().
//
// # Architecture
//
// The rendering model is intentionally simple:
//
//	Window.render(width, isCursor, styles) → string
//
// All caching is internal to Window. Callers don't need to know about
// cache invalidation, line heights, or rebuild states.
//
// # Wrapping Strategy
//
// Line wrapping (wrapContent) happens in each rendering path, NOT in a
// single centralized point. This is intentional for performance:
//
//   - renderGenericContent wraps and caches wrappedLines. AppendContent
//     incrementally updates these cached lines via appendDeltaToLines,
//     keeping the per-append cost at O(delta). Moving wrapping into
//     rebuildCache would make every render O(n) instead of O(delta),
//     destroying the incremental streaming performance.
//
//   - RenderDiffContent wraps per-line to preserve diff coloring (add/remove
//     styles) on wrapped continuations.
//
//   - The tool result path in rebuildCache wraps once since tool results
//     are short, appended once, and have no incremental concern.
//
// Do NOT consolidate these into a single wrapContent call in rebuildCache.
// Each site owns its own wrapping for a reason.
//
// Related files:
//   - window_buffer.go — WindowBuffer, line tracking, virtual rendering
//   - wrap.go — wrapContent, wrapLines, appendDeltaToLines, styleMultiline
//   - display.go — DisplayModel viewport/scroll/cursor

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Window - Single Display Window with Internal Caching
// ============================================================================

// Window represents a single display window with border and content.
// Caching is handled internally - callers just call Render().
//
// For tool windows (ToolName != ""), content is split into ToolInput
// (the tool call arguments) and ToolOutput (the execution result).
// For text windows (AT/AR/UT/SE/SN), Content holds the full text.
type Window struct {
	ID         string     // stream ID or generated unique ID
	Tag        string     // TLV tag that created this window
	ToolName   string     // tool name (for AF/UF tags)
	ToolInput  string     // tool call input (formatted, for AF windows)
	ToolOutput string     // tool execution output (for UF windows)
	Content    string     // accumulated content (raw, unstyled) for non-tool windows
	Folded     bool       // true if window is in folded (collapsed) mode
	Status     ToolStatus // status indicator for tool windows
	Visible    bool       // true if window should be rendered (tool windows always true; delta windows only when has non-whitespace content)
	styles     *Styles    // reference to styles for incremental updates

	// Internal cache - updated on render, invalidated on content change
	cache windowCache
}

// windowCache holds rendered output and derived state
type windowCache struct {
	valid        bool       // true if cache is valid
	width        int        // width used for cached render
	folded       bool       // folded state when cached
	contentLen   int        // content length when cached (for text windows)
	toolInput    string     // tool input when cached (for tool windows)
	toolOutput   string     // tool output when cached (for tool windows)
	toolStatus   ToolStatus // tool status when cached (for tool windows)
	rendered     string     // full output with border
	inner        string     // inner content (for cursor border swap)
	lineCount    int        // number of lines in rendered output
	wrappedLines []string   // wrapped lines for incremental update
}

// IsDiffWindow returns true if the window is a diff window
func (w *Window) IsDiffWindow() bool {
	return w.ToolName == "edit_file"
}

// IsToolWindow returns true if the window is a tool call/result window
func (w *Window) IsToolWindow() bool {
	return w.ToolName != ""
}

// hasVisibleContent returns true if content contains at least one non-whitespace character
func hasVisibleContent(content string) bool {
	for _, r := range content {
		if !isWhitespace(r) {
			return true
		}
	}
	return false
}

// isWhitespace returns true if the character is a whitespace character
func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// Render returns the window with border, using cache if valid.
// This is the single entry point for rendering a window.
func (w *Window) Render(width int, isCursor bool, styles *Styles, borderStyle, cursorStyle lipgloss.Style) string {
	// Check if cache is valid
	cacheValid := w.cache.valid && w.cache.width == width && w.cache.folded == w.Folded
	if cacheValid {
		if w.IsToolWindow() {
			// Tool windows: re-render if input, output, or status changed
			if w.ToolInput != w.cache.toolInput || w.ToolOutput != w.cache.toolOutput || w.Status != w.cache.toolStatus {
				w.cache.valid = false
			}
		} else if len(w.Content) != w.cache.contentLen {
			// Regular windows: re-render if content length changed
			w.cache.valid = false
		}
	} else {
		w.cache.valid = false
	}

	// Rebuild cache if needed
	if !w.cache.valid {
		w.rebuildCache(width, styles, borderStyle)
	}

	// Return with appropriate border
	if isCursor {
		return cursorStyle.Width(width).Render(w.cache.inner)
	}
	return w.cache.rendered
}

// rebuildCache renders the window content and updates the cache
func (w *Window) rebuildCache(width int, styles *Styles, borderStyle lipgloss.Style) {
	innerWidth := max(0, width-BorderInnerPadding)

	var inner string
	switch {
	case w.IsToolWindow():
		inner = w.renderToolContent(innerWidth, styles)
	default:
		inner = w.renderGenericContent(innerWidth, styles, w.Content)
	}

	// Apply folding if needed
	if w.Folded {
		inner = w.applyFolding(inner, innerWidth, styles)
	}

	// Update cache
	w.cache.rendered = borderStyle.Width(width).Render(inner)
	w.cache.inner = inner
	w.cache.width = width
	w.cache.folded = w.Folded
	w.cache.contentLen = len(w.Content)
	w.cache.toolInput = w.ToolInput
	w.cache.toolOutput = w.ToolOutput
	w.cache.toolStatus = w.Status
	w.cache.lineCount = strings.Count(w.cache.rendered, "\n") + 1
	w.cache.valid = true
}

// renderToolContent renders the tool call input (and output if present).
// ToolInput and ToolOutput are separate typed fields — no sentinel parsing needed.
// Only content-heavy tools (write_file, edit_file) show the "OUTPUT:" separator.
func (w *Window) renderToolContent(innerWidth int, styles *Styles) string {
	// Render the call input portion
	var call string
	if w.IsDiffWindow() {
		call = RenderDiffContent(w.ToolInput, w.Status, styles, innerWidth)
	} else {
		call = w.renderGenericContent(innerWidth, styles, w.ToolInput)
	}

	// If a tool result exists, append it
	if w.ToolOutput != "" {
		result := styleMultiline(w.ToolOutput, styles.Text)
		if innerWidth > 0 {
			result = wrapContent(result, innerWidth)
		}
		// Only write_file and edit_file show a separator between input and output
		if w.ToolName == "write_file" || w.ToolName == "edit_file" {
			sep := styles.System.Render("OUTPUT:")
			return call + "\n" + sep + "\n" + result
		}
		// Other tools (execute_command, read_file, search_content) just append.
		// The call content already ends with \n, so no extra separator needed.
		return call + result
	}
	return call
}

// renderGenericContent renders content using styleContent with tag-based styling.
// Wraps and caches wrappedLines so that AppendContent can update them
// incrementally (O(delta) per append instead of O(n) full re-wrap).
// Do NOT move wrapping out of this function — see "Wrapping Strategy" above.
func (w *Window) renderGenericContent(innerWidth int, styles *Styles, content string) string {
	innerWidth = max(0, innerWidth)

	// FAST PATH: Use cached wrapped lines if width matches
	// This avoids re-styling and re-wrapping the entire content
	if len(w.cache.wrappedLines) > 0 && w.cache.width-BorderInnerPadding == innerWidth && innerWidth > 0 {
		return strings.Join(w.cache.wrappedLines, "\n")
	}

	// SLOW PATH: Full styling and wrapping

	// Prepare content: strip ANSI and expand tabs
	content = prepareContent(content)

	// Apply styling based on tag
	content = w.styleContent(content, styles)

	// Wrap content
	if innerWidth <= 0 {
		w.cache.wrappedLines = nil
		return content
	}

	wrapped := wrapContent(content, innerWidth)
	w.cache.wrappedLines = strings.Split(wrapped, "\n")
	return wrapped
}

// applyFolding collapses content to first 2 lines + indicator + last 2 lines
// The indicator (splitter row) sits at row 3, the center of the 5-line folded window.
func (w *Window) applyFolding(content string, innerWidth int, styles *Styles) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 5 {
		return content
	}

	indicator := lipgloss.NewStyle().
		Foreground(styles.ColorDim).
		Render(strings.Repeat(styles.FoldIndicator, innerWidth))

	return lines[0] + "\n" + lines[1] + "\n" + indicator + "\n" + strings.Join(lines[len(lines)-2:], "\n")
}

// Invalidate marks the cache as invalid (called when content changes)
func (w *Window) Invalidate() {
	w.cache.valid = false
	w.cache.wrappedLines = nil
}

// AppendContent adds content incrementally, updating wrapped lines if possible.
// This is the key to O(delta) streaming performance — it avoids re-wrapping
// the entire content on every render. See "Wrapping Strategy" above.
func (w *Window) AppendContent(delta string, innerWidth int) {
	w.Content += delta

	// Try incremental update if we have cached wrapped lines and styles
	if len(w.cache.wrappedLines) > 0 && innerWidth > 0 && w.styles != nil {
		// Prepare delta before styling (strip input ANSI, expand tabs)
		preparedDelta := prepareContent(delta)
		styledDelta := w.styleContent(preparedDelta, w.styles)
		w.cache.wrappedLines = appendDeltaToLines(w.cache.wrappedLines, styledDelta, innerWidth)
		// Mark cache as needing rebuild for rendered output, but wrappedLines is updated
		// The rebuild will use cached wrappedLines instead of re-wrapping
		w.cache.valid = false
	} else {
		// Can't do incremental - need full rebuild
		w.cache.valid = false
		w.cache.wrappedLines = nil
	}
}

// styleContent applies styling to content based on window tag
func (w *Window) styleContent(content string, styles *Styles) string {
	if styles == nil {
		return content
	}

	// Apply styling based on tag
	switch w.Tag {
	case stream.TagFunction:
		prefix := w.Status.Indicator(styles)
		return prefix + ColorizeTool(content, styles)
	case stream.TagFunctionResult:
		return styleMultiline(content, styles.Text)
	case stream.TagTextAssistant:
		return styleMultiline(content, styles.Text)
	case stream.TagTextReasoning:
		return styleMultiline(content, styles.Reasoning)
	case stream.TagTextUser:
		return styles.Prompt.Render("> ") + styles.UserInput.Render(content)
	case stream.TagSystemError:
		return styleMultiline(content, styles.Error)
	case stream.TagSystemNotify:
		return styleMultiline(content, styles.System)
	default:
		return content
	}
}

// LineCount returns the cached line count (valid after Render())
func (w *Window) LineCount() int {
	return w.cache.lineCount
}
