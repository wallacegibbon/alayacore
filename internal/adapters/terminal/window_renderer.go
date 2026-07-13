package terminal

// Window renderers: type-specific content management and rendering.
//
// Each renderer implements WindowRendering and owns its content storage
// and caching. The Window struct delegates to the renderer for everything
// that varies by window type.

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alayacore/alayacore/internal/tlv"
)

// ============================================================================
// textRenderer — Assistant text (AT), reasoning (AR), system messages (SN/SE)
// ============================================================================

// textRenderer handles simple text content with optional streaming deltas.
// Used for AT, AR, SN, and SE tags.
type textRenderer struct {
	tag          string   // TLV tag that created this window
	content      string   // full content (built from parts on demand)
	contentLen   int      // cumulative length of all deltas
	contentParts []string // streaming deltas (avoids O(n²) string concat)

	// Cached wrapped lines for fast incremental update via appendDeltaToLines.
	// Populated by BuildInner, updated incrementally by AppendFromTLV.
	wrappedLines []string
	cacheWidth   int     // inner width used for wrapping (0 = unknown)
	cacheValid   bool    // true = BuildInner can skip full re-wrap
	lastStyles   *Styles // cached styles reference for incremental append
}

func (r *textRenderer) Tag() string { return r.tag }

func (r *textRenderer) ToolInfo() *ToolInfo { return nil }

func (r *textRenderer) AppendFromTLV(_ string, value string) {
	r.contentParts = append(r.contentParts, value)
	r.contentLen += len(value)

	// Incremental update: style and wrap the delta, append to wrappedLines.
	// This avoids O(n) full re-wrap on every streaming frame.
	if len(r.wrappedLines) > 0 && r.cacheWidth > 0 && r.lastStyles != nil {
		prepared := prepareContent(value)
		styled := styleByTag(r.tag, prepared, r.lastStyles, "")
		r.wrappedLines = appendDeltaToLines(r.wrappedLines, styled, r.cacheWidth)
		// cacheValid stays false — border cache needs rebuild,
		// but wrappedLines is current for TryLineCount.
	} else {
		r.cacheValid = false
	}
}

func (r *textRenderer) Invalidate() {
	r.cacheValid = false
	r.wrappedLines = nil
}

// rawContent returns the full accumulated content for testing.
func (r *textRenderer) rawContent() string {
	if len(r.contentParts) > 0 {
		var buf strings.Builder
		buf.WriteString(r.content)
		for _, p := range r.contentParts {
			buf.WriteString(p)
		}
		return buf.String()
	}
	return r.content
}

// BuildInner returns the styled inner content.
// For textRenderer, this applies tag-based styling and wraps to width.
func (r *textRenderer) BuildInner(width int, _ bool, styles *Styles) (string, int) {
	innerWidth := max(0, width-BorderInnerPadding)

	// Fast path: use cached wrapped lines if width matches.
	// wrappedLines is kept current by AppendFromTLV's incremental path.
	if r.cacheWidth == innerWidth && len(r.wrappedLines) > 0 {
		// Still merge contentParts for eventual consistency (resize, slow path).
		// This prevents unbounded growth during long streaming sessions.
		if len(r.contentParts) > 0 {
			var buf strings.Builder
			buf.WriteString(r.content)
			for _, part := range r.contentParts {
				buf.WriteString(part)
			}
			r.content = buf.String()
			r.contentParts = nil
		}
		r.lastStyles = styles
		return strings.Join(r.wrappedLines, "\n"), len(r.wrappedLines) + 2
	}

	// Full render: prepare, style, and wrap
	// Ensure full content from parts
	if len(r.contentParts) > 0 {
		var buf strings.Builder
		buf.WriteString(r.content)
		for _, part := range r.contentParts {
			buf.WriteString(part)
		}
		r.content = buf.String()
		r.contentParts = nil
	}

	content := prepareContent(r.content)
	content = styleByTag(r.tag, content, styles, "")

	if innerWidth <= 0 {
		lineCount := strings.Count(content, "\n") + 1
		return content, lineCount + 2
	}

	wrapped := wrapContent(content, innerWidth)
	r.wrappedLines = strings.Split(wrapped, "\n")
	r.cacheWidth = innerWidth
	r.lastStyles = styles
	r.cacheValid = true

	return wrapped, len(r.wrappedLines) + 2
}

// TryLineCount returns the line count from cached wrapped lines (fast path).
// With incremental append, wrappedLines is kept current during streaming,
// so this succeeds even after content changes (no cacheValid check).
func (r *textRenderer) TryLineCount(width int) (int, bool) {
	innerWidth := max(0, width-BorderInnerPadding)
	if len(r.wrappedLines) > 0 && r.cacheWidth == innerWidth {
		return len(r.wrappedLines) + 2, true
	}
	return 0, false
}

// ============================================================================
// userRenderer — User messages with optional media attachments (UT)
// ============================================================================

// userRenderer handles user messages that may include media attachments.
// Text parts and media labels are stored separately and combined at render time.
type userRenderer struct {
	textParts  []string // user text, in order
	mediaParts []string // media labels, in order
	contentLen int
}

func (r *userRenderer) Tag() string { return tlv.TagUserT }

func (r *userRenderer) ToolInfo() *ToolInfo { return nil }

func (r *userRenderer) AppendFromTLV(tag string, value string) {
	switch tag {
	case tlv.TagUserT:
		if value != "" {
			r.textParts = append(r.textParts, value)
		}
	case tlv.TagUserI:
		r.mediaParts = append(r.mediaParts, "📎 Image")
	case tlv.TagUserV:
		r.mediaParts = append(r.mediaParts, "🎬 Video")
	case tlv.TagUserA:
		r.mediaParts = append(r.mediaParts, "🎵 Audio")
	case tlv.TagUserD:
		r.mediaParts = append(r.mediaParts, "📄 Document")
	}
	r.contentLen += len(value)
}

func (r *userRenderer) Invalidate() {}

// BuildInner renders the user message: media section first (on top), then text below.
// This matches the natural content order: media parts precede the text part.
// Multiple text parts are separated with "---" in System color.
func (r *userRenderer) BuildInner(width int, _ bool, styles *Styles) (string, int) {
	innerWidth := max(0, width-BorderInnerPadding)
	media := strings.Join(r.mediaParts, "  ")

	var parts []string

	// Media portion — rendered first (on top)
	if media != "" {
		var mediaBlock strings.Builder
		mediaBlock.WriteString(styles.Attachment.Render(media))
		if innerWidth > 0 {
			mediaBlockStr := wrapMediaLabels(mediaBlock.String(), innerWidth, styles)
			parts = append(parts, mediaBlockStr)
		} else {
			parts = append(parts, mediaBlock.String())
		}
	}

	// Text portion: text parts separated by "---"
	if len(r.textParts) > 0 {
		var textBlock strings.Builder

		// Separate from media with "---"
		if media != "" {
			textBlock.WriteString(styles.System.Render(Separator))
			textBlock.WriteString("\n")
		}

		firstText := true
		for _, part := range r.textParts {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if !firstText {
				textBlock.WriteString("\n")
				textBlock.WriteString(styles.System.Render(Separator))
				textBlock.WriteString("\n")
			}
			textBlock.WriteString(styles.UserInput.Render(trimmed))
			firstText = false
		}

		if textBlock.Len() > 0 {
			styledText := textBlock.String()
			if innerWidth > 0 {
				styledText = wrapContent(styledText, innerWidth)
			}
			parts = append(parts, styledText)
		}
	}

	result := strings.Join(parts, "\n")

	// Count lines (add 2 for border)
	lineCount := strings.Count(result, "\n") + 1 + 2
	return result, lineCount
}

// wrapMediaLabels wraps styled media labels at word boundaries (double spaces),
// preserving ANSI styles. Each label is kept intact; wrapping only occurs at
// the "  " separator between labels.
func wrapMediaLabels(s string, width int, styles *Styles) string {
	if width < 1 {
		return s
	}
	// Split into individual labels by the double-space separator.
	// We strip ANSI first, split, then re-style each label.
	plain := ansi.Strip(s)
	labels := strings.Split(plain, "  ")

	var lines []string
	var currentLine strings.Builder

	for i, label := range labels {
		if label == "" {
			continue
		}
		labelWidth := ansi.StringWidth(label)
		if currentLine.Len() > 0 {
			sepWidth := 2 // "  "
			if ansi.StringWidth(currentLine.String())+sepWidth+labelWidth > width {
				// Flush current line and start a new one
				// Re-apply attachment style to the raw line text
				styled := styles.Attachment.Render(currentLine.String())
				lines = append(lines, styled)
				currentLine.Reset()
				currentLine.WriteString(label)
			} else {
				if currentLine.Len() > 0 {
					currentLine.WriteString("  ")
				}
				currentLine.WriteString(label)
			}
		} else {
			currentLine.WriteString(label)
		}
		// If it's the last label, flush
		if i == len(labels)-1 && currentLine.Len() > 0 {
			styled := styles.Attachment.Render(currentLine.String())
			lines = append(lines, styled)
		}
	}

	return strings.Join(lines, "\n")
}

// ============================================================================
// toolRenderer — Tool calls and results (AF, UF)
// ============================================================================

// toolRenderer handles tool call windows that show input and optional output.
type toolRenderer struct {
	isUF   bool   // true for UF-only windows (no prior AF frame)
	name   string // tool name (e.g. "read_file")
	input  string // formatted tool call input (complete, from AF)
	output string // tool execution output
	status ToolStatus

	// deltaBuffer accumulates partial JSON from Af frames during streaming.
	// Not appended to window content — rendered as a one-line preview
	// alongside the tool name in the pending state.
	deltaBuffer string
}

func (r *toolRenderer) Tag() string { return tlv.TagAssistantF }

// showSeparator returns true if the tool should display a separator
// between the call input and its result. Only diff-style tools (edit_file,
// write_file) show a separator — other tools append output directly.
func (r *toolRenderer) showSeparator() bool {
	return r.name == "edit_file" || r.name == "write_file"
}

func (r *toolRenderer) ToolInfo() *ToolInfo {
	return &ToolInfo{
		Name:  r.name,
		Input: r.input,
	}
}

func (r *toolRenderer) AppendFromTLV(_ string, value string) {
	// Tool data normally arrives via structured setters (HandleToolInput/HandleToolOutput).
	// For replayed content or direct testing, dispatch by window type.
	if r.isUF {
		r.output = value
	} else {
		r.input = value
	}
}

func (r *toolRenderer) Invalidate() {}

// AppendDelta sets the latest partial JSON chunk for one-line preview.
// Each call replaces the previous delta — the window shows only the
// most recently received chunk alongside the tool name.
func (r *toolRenderer) AppendDelta(delta string) {
	r.deltaBuffer = delta
}

func (r *toolRenderer) BuildInner(width int, _ bool, styles *Styles) (string, int) {
	innerWidth := max(0, width-BorderInnerPadding)

	// UF-only windows (no tool name, created from UF tag) render as plain text.
	if r.isUF {
		styled := styleMultiline(prepareContent(r.output), styles.Text)
		if innerWidth > 0 {
			styled = wrapContent(styled, innerWidth)
		}
		return styled, strings.Count(styled, "\n") + 1 + 2
	}

	// Input still streaming via Af deltas — show truncated one-line preview
	// of accumulated arguments alongside the tool name.
	if r.deltaBuffer != "" {
		// Status dot uses its normal color, tool name uses Tool style (golden),
		// colon and delta content use ToolContent style (muted), truncated to one line.
		deltaContent := r.deltaBuffer
		// Flatten delta to single line.
		deltaContent = strings.ReplaceAll(deltaContent, "\n", " ")
		deltaContent = strings.ReplaceAll(deltaContent, "\r", "")
		// Truncate to fit available width (indicator + name + ": " + delta).
		// Reserve ~5 chars for "• " and ": " and "…".
		maxDelta := max(0, innerWidth-lipgloss.Width(r.name)-5)
		if len(deltaContent) > maxDelta {
			deltaContent = deltaContent[:maxDelta] + "…"
		}
		display := r.status.Indicator(styles) + styles.Tool.Render(r.name) + styles.ToolContent.Render(": "+deltaContent)
		return display, strings.Count(display, "\n") + 1 + 2
	}

	// Full tool rendering: input (with indicator) + optional output
	var renderFn func(string, ToolStatus, *Styles, int) string

	switch r.name {
	case "edit_file":
		renderFn = RenderDiffContent
	default:
		renderFn = defaultToolRender
	}

	// Render input
	call := renderFn(r.input, r.status, styles, innerWidth)

	// Append output if present, with separator only for diff-style tools
	if r.output != "" && r.showSeparator() {
		var result strings.Builder
		result.WriteString(call)

		sep := styles.System.Render(Separator)
		styled := styleMultiline(prepareContent(r.output), styles.Text)
		if innerWidth > 0 {
			styled = wrapContent(styled, innerWidth)
		}
		result.WriteString("\n")
		result.WriteString(sep)
		result.WriteString("\n")
		result.WriteString(styled)

		content := result.String()
		return content, strings.Count(content, "\n") + 1 + 2
	}

	// No separator — append output directly after input
	if r.output != "" {
		styled := styleMultiline(prepareContent(r.output), styles.Text)
		if innerWidth > 0 {
			styled = wrapContent(styled, innerWidth)
		}
		call += styled
		return call, strings.Count(call, "\n") + 1 + 2
	}

	return call, strings.Count(call, "\n") + 1 + 2
}

// defaultToolRender renders a tool call with status indicator and coloring.
func defaultToolRender(input string, status ToolStatus, styles *Styles, innerWidth int) string {
	content := prepareContent(input)
	content = status.Indicator(styles) + ColorizeTool(content, styles)
	if innerWidth > 0 {
		content = wrapContent(content, innerWidth)
	}
	return content
}

// ============================================================================
// Style dispatch (replaces the old applyTagStyle switch)
// ============================================================================

// styleByTag applies styling based on the window's TLV tag.
// mediaContent is only relevant for TagUserT windows.
func styleByTag(tag, content string, styles *Styles, _ string) string {
	if styles == nil {
		return content
	}

	switch tag {
	case tlv.TagAssistantF:
		return ColorizeTool(content, styles)
	case tlv.TagUserF:
		return styleMultiline(content, styles.Text)
	case tlv.TagAssistantT:
		return styleMultiline(content, styles.Text)
	case tlv.TagAssistantR:
		return styleMultiline(content, styles.Reasoning)
	case tlv.TagUserI, tlv.TagUserV, tlv.TagUserA, tlv.TagUserD:
		return styleMultiline(content, styles.Attachment)
	case tlv.TagUserT:
		// User text without media is styled by userRenderer directly
		// This path is for fallback only (e.g. replayed content)
		if content == "" {
			return ""
		}
		return styles.UserInput.Render(content)
	case TagWindowSE:
		return styleMultiline(content, styles.Error)
	case TagWindowSN:
		return styleMultiline(content, styles.System)
	default:
		return content
	}
}
