package stream

import (
	"strconv"
	"strings"
)

// StreamID identifies a delta stream across multiple TLV frames.
//
// Wire format within a TLV value:
//
//	\x00<id>\x00<content>
//
// The NUL byte (\x00) is used as the delimiter because it can never appear
// in normal UTF-8 text content, making the split unambiguous regardless of
// what the LLM generates.
//
// The id string itself follows the convention:
//
//	"<promptID>-<step>-<suffix>"
//
// where suffix is one of the Suffix* constants below, or a free-form tool
// call ID for TagFunctionState messages.

// Suffix constants for StreamID construction.
const (
	SuffixText      = "t" // Assistant text delta  (TagTextAssistant)
	SuffixReasoning = "r" // Reasoning delta       (TagTextReasoning)
)

// NewStreamID constructs a stream ID string from components.
func NewStreamID(promptID uint64, step int, suffix string) string {
	return strconv.FormatUint(promptID, 10) + "-" +
		strconv.FormatInt(int64(step), 10) + "-" +
		suffix
}

// WrapDelta prepends the NUL-delimited stream ID prefix to content.
// Result: \x00<id>\x00<content>
func WrapDelta(id string, content string) string {
	return "\x00" + id + "\x00" + content
}

// UnwrapDelta splits a NUL-delimited TLV value into (streamID, content).
//
// It expects the format \x00<id>\x00<content>. If the value does not start
// with \x00 or the closing \x00 is not found, it returns ("", value, false).
//
// Callers must handle the ok=false case: it occurs when messages are
// replayed from a saved session file (which stores plain text without
// stream IDs) or when the value is empty/malformed.
//
// The returned id is guaranteed to be non-empty when ok is true.
func UnwrapDelta(value string) (id string, content string, ok bool) {
	if len(value) == 0 || value[0] != 0 {
		return "", value, false
	}

	// Find the second NUL byte (index 0 is the first)
	endIdx := strings.IndexByte(value[1:], 0)
	if endIdx == -1 {
		return "", value, false
	}
	endIdx++ // adjust for the slice offset

	id = value[1:endIdx]
	if id == "" {
		return "", value, false
	}

	return id, value[endIdx+1:], true
}
