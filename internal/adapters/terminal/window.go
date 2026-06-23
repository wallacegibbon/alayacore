package terminal

// Window is a single display unit with border and content.
// Caching is handled internally — callers just call Render().
//
// # Architecture
//
// The rendering model is intentionally simple:
//
//	Window.Render(width, isCursor, styles) → string
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
//
// During streaming, content deltas are accumulated in contentParts to
// avoid O(n²) string concatenation from repeated Content += delta.
// The full Content string is only built when a full cache rebuild
// is needed (resize, style change, etc.).
type Window struct {
	ID           string     // stream ID or generated unique ID
	HistoryID    uint64     // numeric history ID from the stream
	Tag          string     // TLV tag that created this window
	ToolName     string     // tool name (for AF/UF tags)
	ToolInput    string     // tool call input (formatted, for AF windows)
	ToolOutput   string     // tool execution output (for UF windows)
	Content      string     // accumulated content — built from contentParts on demand
	MediaContent string     // media labels (for user windows with attachments)
	Folded       bool       // true if window is in folded (collapsed) mode
	Status       ToolStatus // status indicator for tool windows
	Visible      bool       // true if window should be rendered (delta windows only when has non-whitespace content)
	styles       *Styles    // reference to styles for incremental updates

	// contentParts accumulates streaming deltas to avoid O(n²)
	// from repeated Content += delta. Joined into Content on full rebuild.
	contentParts []string
	contentLen   int // cumulative length of all deltas

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
		} else if w.contentLen != w.cache.contentLen {
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

	// Build full Content from parts if a full rebuild is needed.
	// During normal streaming, Content is empty because deltas are
	// accumulated in contentParts to avoid O(n²) string concatenation.
	w.ensureContent()

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
// Rendering is delegated to the tool's ToolRenderer.
func (w *Window) renderToolContent(innerWidth int, styles *Styles) string {
	var r ToolRenderer
	switch w.ToolName {
	case "edit_file":
		r = diffRenderer{}
	case "write_file":
		r = outputSeparatorRenderer{}
	default:
		r = defaultRenderer{}
	}

	// Render the call input portion using the tool-specific renderer
	call := r.RenderInput(w.ToolInput, w.Status, styles, innerWidth)

	// No tool result — just the call input
	if w.ToolOutput == "" {
		return call
	}

	// Append tool result with optional separator
	result := styleMultiline(prepareContent(w.ToolOutput), styles.Text)
	if innerWidth > 0 {
		result = wrapContent(result, innerWidth)
	}
	if r.ShowOutputSeparator() {
		sep := styles.System.Render("OUTPUT:")
		return call + "\n" + sep + "\n" + result
	}
	return call + result
}

// renderGenericContent renders content using applyTagStyle with tag-based styling.
// Wraps and caches wrappedLines so that AppendContent can update them
// incrementally (O(delta) per append instead of O(n) full re-wrap).
// Do NOT move wrapping out of this function — see "Wrapping Strategy" above.
func (w *Window) renderGenericContent(innerWidth int, styles *Styles, content string) string {
	innerWidth = max(0, innerWidth)

	// FAST PATH: Use cached wrapped lines if inner width matches.
	// cache.width stores the outer (border-inclusive) width, so subtract
	// BorderInnerPadding to compare against the requested inner width.
	if len(w.cache.wrappedLines) > 0 && w.cache.width-BorderInnerPadding == innerWidth && innerWidth > 0 {
		return strings.Join(w.cache.wrappedLines, "\n")
	}

	// SLOW PATH: Full styling and wrapping

	// Prepare content: strip ANSI and expand tabs
	content = prepareContent(content)

	// Apply styling based on tag
	content = w.applyTagStyle(content, styles)

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

// UpdateLineCount computes the line count from cached wrapped lines
// without re-rendering the border. This is ~58μs faster than Render()
// during streaming, called from ensureLineHeights when only the line
// count is needed (not the rendered string).
//
// Note: This used to join all wrapped lines to count newlines, but since
// wrappedLines stores one line per element, len(wrappedLines) is the
// exact line count. This avoids an O(n) string join on every update.
//
// The returned count includes the 2 border lines (top and bottom) to
// match what rebuildCache computes via strings.Count(rendered, "\n")+1.
// See window_cache struct: lineCount = total lines in rendered output.
//
// Returns true if the line count was successfully updated from cached
// wrappedLines (fast path). Returns false if wrappedLines was nil or
// empty — the caller should fall back to a full Render.
//
// CAVEAT: For tool windows with output, wrappedLines only holds the
// tool input portion — the output is rendered separately and never
// stored there. This function returns false for such windows so the
// caller falls through to the full Render(), which counts correctly.
func (w *Window) UpdateLineCount(width int) bool {
	innerWidth := max(0, width-BorderInnerPadding)

	if w.IsToolWindow() && w.ToolOutput != "" {
		w.cache.valid = false
		return false
	}

	if len(w.cache.wrappedLines) > 0 && innerWidth > 0 {
		contentLines := len(w.cache.wrappedLines)
		if w.Folded {
			// Folded windows show at most 5 lines (2 top + indicator + 2 bottom).
			contentLines = min(5, contentLines)
		}
		// Add 2 for border (top + bottom lines rendered by lipgloss).
		// rebuildCache computes: lineCount = strings.Count(rendered, "\n") + 1
		// where rendered = borderStyle.Width(width).Render(inner) adds 2 lines.
		w.cache.lineCount = contentLines + 2
		// Mark rendered as stale — will be rebuilt on next Render() call
		w.cache.valid = false
		return true
	}

	// No cached wrapped lines — fall back to full render
	w.cache.valid = false
	return false
}

// ensureContent builds w.Content from contentParts if not already built.
// This is called by accessors that need the full content string.
func (w *Window) ensureContent() {
	if len(w.contentParts) > 0 {
		// Content (if any) was set before contentParts started accumulating.
		// Parts are appended in order after Content.
		var buf strings.Builder
		buf.WriteString(w.Content)
		for _, part := range w.contentParts {
			buf.WriteString(part)
		}
		w.Content = buf.String()
		w.contentParts = nil // allow GC
	}
}

// AppendContent adds content incrementally, updating wrapped lines if possible.
// This is the key to O(delta) streaming performance — it avoids re-wrapping
// the entire content on every render. See "Wrapping Strategy" above.
//
// Content deltas are accumulated in contentParts to avoid O(n²) from
// repeated Content += delta. The full Content string is joined on demand
// in rebuildCache when a full rebuild is needed.
func (w *Window) AppendContent(delta string, innerWidth int) {
	w.contentParts = append(w.contentParts, delta)
	w.contentLen += len(delta)

	// Try incremental update if we have cached wrapped lines and styles
	if len(w.cache.wrappedLines) > 0 && innerWidth > 0 && w.styles != nil {
		// Prepare delta before styling (strip input ANSI, expand tabs)
		preparedDelta := prepareContent(delta)
		styledDelta := w.applyTagStyle(preparedDelta, w.styles)
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

// applyTagStyle applies styling to content based on window tag
func (w *Window) applyTagStyle(content string, styles *Styles) string {
	if styles == nil {
		return content
	}

	// Apply styling based on tag
	switch w.Tag {
	case stream.TagAssistantF:
		prefix := w.Status.Indicator(styles)
		return prefix + ColorizeTool(content, styles)
	case stream.TagUserF:
		return styleMultiline(content, styles.Text)
	case stream.TagAssistantT:
		return styleMultiline(content, styles.Text)
	case stream.TagAssistantR:
		return styleMultiline(content, styles.Reasoning)
	case stream.TagUserI:
		return styleMultiline(content, styles.Attachment)
	case stream.TagUserV:
		return styleMultiline(content, styles.Attachment)
	case stream.TagUserA:
		return styleMultiline(content, styles.Attachment)
	case stream.TagUserD:
		return styleMultiline(content, styles.Attachment)
	case stream.TagUserT:
		result := styles.Prompt.Render("> ")
		if content != "" {
			result += styles.UserInput.Render(content)
		}
		if w.MediaContent != "" {
			result += "\n" + styles.System.Render("MEDIA:") + "\n" +
				styleMultiline(w.MediaContent, styles.Attachment)
		}
		return result
	case TagWindowSE:
		return styleMultiline(content, styles.Error)
	case TagWindowSN:
		return styleMultiline(content, styles.System)
	default:
		return content
	}
}

// LineCount returns the cached line count (valid after Render())
func (w *Window) LineCount() int {
	return w.cache.lineCount
}
