package terminal

// Window renderers: type-specific content management and rendering.
//
// Each renderer implements WindowRendering and owns its content storage
// and caching. The Window struct delegates to the renderer for everything
// that varies by window type.

import (
	"strings"

	"github.com/alayacore/alayacore/internal/stream"
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

func (r *userRenderer) Tag() string { return stream.TagUserT }

func (r *userRenderer) ToolInfo() *ToolInfo { return nil }

func (r *userRenderer) AppendFromTLV(tag string, value string) {
	switch tag {
	case stream.TagUserT:
		if value != "" {
			r.textParts = append(r.textParts, value)
		}
	case stream.TagUserI:
		r.mediaParts = append(r.mediaParts, "📎 Image")
	case stream.TagUserV:
		r.mediaParts = append(r.mediaParts, "🎬 Video")
	case stream.TagUserA:
		r.mediaParts = append(r.mediaParts, "🎵 Audio")
	case stream.TagUserD:
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
			mediaBlockStr := wrapContent(mediaBlock.String(), innerWidth)
			parts = append(parts, mediaBlockStr)
		} else {
			parts = append(parts, mediaBlock.String())
		}
	}

	// Text portion: each text part gets "> " prefix, separated by "---"
	if len(r.textParts) > 0 {
		var textBlock strings.Builder

		for _, part := range r.textParts {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if textBlock.Len() > 0 {
				textBlock.WriteString("\n")
				textBlock.WriteString(styles.System.Render(Separator))
				textBlock.WriteString("\n")
			}
			textBlock.WriteString(styles.Prompt.Render("> "))
			textBlock.WriteString(styles.UserInput.Render(trimmed))
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

// ============================================================================
// toolRenderer — Tool calls and results (AF, UF)
// ============================================================================

// toolRenderer handles tool call windows that show input and optional output.
type toolRenderer struct {
	isUF   bool   // true for UF-only windows (no prior AF frame)
	name   string // tool name (e.g. "read_file")
	input  string // formatted tool call input
	output string // tool execution output
	status ToolStatus
}

func (r *toolRenderer) Tag() string { return stream.TagAssistantF }

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
	case stream.TagAssistantF:
		return ColorizeTool(content, styles)
	case stream.TagUserF:
		return styleMultiline(content, styles.Text)
	case stream.TagAssistantT:
		return styleMultiline(content, styles.Text)
	case stream.TagAssistantR:
		return styleMultiline(content, styles.Reasoning)
	case stream.TagUserI, stream.TagUserV, stream.TagUserA, stream.TagUserD:
		return styleMultiline(content, styles.Attachment)
	case stream.TagUserT:
		// User text without media is styled by userRenderer directly
		// This path is for fallback only (e.g. replayed content)
		result := styles.Prompt.Render("> ")
		if content != "" {
			result += styles.UserInput.Render(content)
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
