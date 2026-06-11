package stream

import (
	"strconv"
	"strings"
)

// StreamID identifies a delta stream across multiple TLV frames.
// Used by AT, AR, UT, UI, AF, and UF to carry a historyCount-based ID.
//
// Wire format within a TLV value:
//
//	\x00<id>\x00<content>
//
// The NUL byte (\x00) is used as the delimiter because it can never appear
// in normal UTF-8 text content, making the split unambiguous regardless of
// what the LLM generates.
//
// The id is a decimal number from the session's history counter.

// NewStreamID constructs a stream ID string from components.
func NewStreamID(promptID uint64, step int, index int) string {
	return strconv.FormatUint(promptID, 10) + "|" +
		strconv.FormatInt(int64(step), 10) + "|" +
		strconv.FormatInt(int64(index), 10)
}

// WrapDelta prepends the NUL-delimited stream ID prefix to content.
// Result: \x00<id>\x00<content>
func WrapDelta(id string, content string) string {
	return "\x00" + id + "\x00" + content
}

// UnwrapDelta splits a NUL-delimited TLV value into (historyID, content).
//
// It expects the format \x00<id>\x00<content>. If the value does not start
// with \x00 or the closing \x00 is not found, it returns ("", value, false).
//
// Callers must handle the ok=false case: it occurs when messages are
// replayed from a saved session file (which stores plain text without
// history IDs) or when the value is empty/malformed.
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
