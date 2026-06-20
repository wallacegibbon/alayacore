package plainio

import (
	"bytes"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestNewlineBetweenDifferentStreamGroups(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Simulate: assistant text delta with NUL-delimited stream IDs
	msg1 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-1-t", "hello "))
	msg2 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-1-t", "world"))
	// New step: different stream ID
	msg3 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-2-t", "new step"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	want := "hello world\nnew step"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNoNewlineWithinSameStreamGroup(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-1-t", "hello "))
	msg2 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-1-t", "world"))

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "hello world"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNewlineBetweenTextAndReasoning(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-1-t", "some text"))
	msg2 := stream.EncodeTLV(stream.TagAssistantR, stream.WrapDelta("0-1-r", "some reasoning"))

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "some text\nsome reasoning"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNewlineBetweenReasoningAndText(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := stream.EncodeTLV(stream.TagAssistantR, stream.WrapDelta("0-1-r", "thinking..."))
	msg2 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-2-t", "answer"))

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "thinking...\nanswer"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestNoPrefixNoNewline(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Messages without stream prefixes are treated as complete text parts
	// (session load style) and each ends with a newline.
	msg1 := stream.EncodeTLV(stream.TagAssistantT, "hello ")
	msg2 := stream.EncodeTLV(stream.TagAssistantT, "world")

	o.Write(msg1)
	o.Write(msg2)

	got := buf.String()
	want := "hello \nworld\n"
	if got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestToolCallResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	// Stream some text
	msg1 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-1-t", "hello"))
	// Then a tool call (resets prefix)
	msg2 := stream.EncodeTLV(stream.TagAssistantF, `{"id":"1","type":"call","name":"read_file","input":"{}"}`)
	// Then more text with different prefix — should NOT get extra newline since tool call reset it
	msg3 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-3-t", "result"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	// After tool call, lastHistoryID is "" so the new ID doesn't trigger separator
	if !contains(got, "hello") || !contains(got, "result") {
		t.Errorf("output = %q", got)
	}
}

func TestUserPromptResetsStreamPrefix(t *testing.T) {
	var buf bytes.Buffer
	o := &stdoutOutput{
		writer: &buf,
	}

	msg1 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("0-1-t", "response"))
	msg2 := stream.EncodeTLV(stream.TagUserT, "next prompt")
	msg3 := stream.EncodeTLV(stream.TagAssistantT, stream.WrapDelta("1-1-t", "new response"))

	o.Write(msg1)
	o.Write(msg2)
	o.Write(msg3)

	got := buf.String()
	if !contains(got, "response") || !contains(got, "new response") {
		t.Errorf("output = %q", got)
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
