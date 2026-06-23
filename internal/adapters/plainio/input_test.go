package plainio

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestReadPrompts_SingleLine(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should emit TLV(UT, "hello") followed by MB
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "hello" {
		t.Errorf("expected value 'hello', got %q", value)
	}
	// MB
	tag, _, err = stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read MB TLV: %v", err)
	}
	if tag != stream.TagMessageBoundary {
		t.Errorf("expected MB tag, got %s", tag)
	}
}

func TestReadPrompts_MultiLineBackslash(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("first line\\\nsecond line\\\nthird line\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should emit TLV(UT, "first line\nsecond line\nthird line") followed by MB
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "first line\nsecond line\nthird line" {
		t.Errorf("expected multi-line value, got %q", value)
	}
	// MB
	tag, _, err = stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read MB TLV: %v", err)
	}
	if tag != stream.TagMessageBoundary {
		t.Errorf("expected MB tag, got %s", tag)
	}
}

func TestReadPrompts_TrailingBackslash(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\\\n")
	// Trailing backslash at EOF with no continuation — the backslash
	// is consumed, leaving "hello" as the accumulated text.

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should emit TLV(UT, "hello") followed by MB
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "hello" {
		t.Errorf("expected 'hello', got %q", value)
	}
	// MB
	tag, _, err = stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read MB TLV: %v", err)
	}
	if tag != stream.TagMessageBoundary {
		t.Errorf("expected MB tag, got %s", tag)
	}
}

func TestReadPrompts_MultipleLines(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("first\nsecond\nthird\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each prompt is followed by MB.
	for i, expected := range []string{"first", "second", "third"} {
		tag, value, err := stream.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read TLV: %v", i, err)
		}
		if tag != stream.TagUserT {
			t.Errorf("prompt %d: expected tag UT, got %s", i, tag)
		}
		if value != expected {
			t.Errorf("prompt %d: expected %q, got %q", i, expected, value)
		}
		// MB
		tag, _, err = stream.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read MB TLV: %v", i, err)
		}
		if tag != stream.TagMessageBoundary {
			t.Errorf("prompt %d: expected MB tag, got %s", i, tag)
		}
	}
}

func TestReadPrompts_EmptyLines(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\n\n\nworld\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each prompt is followed by MB.
	for i, expected := range []string{"hello", "world"} {
		tag, value, err := stream.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read TLV: %v", i, err)
		}
		if tag != stream.TagUserT {
			t.Errorf("prompt %d: expected tag UT, got %s", i, tag)
		}
		if value != expected {
			t.Errorf("prompt %d: expected %q, got %q", i, expected, value)
		}
		// MB
		tag, _, err = stream.ReadTLV(&buf)
		if err != nil {
			t.Fatalf("prompt %d: failed to read MB TLV: %v", i, err)
		}
		if tag != stream.TagMessageBoundary {
			t.Errorf("prompt %d: expected MB tag, got %s", i, tag)
		}
	}

	// Should be no more data
	if buf.Len() > 0 {
		t.Errorf("expected no more data after last prompt, got %d bytes", buf.Len())
	}
}

func TestReadPrompts_EOFPartialLine(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("partial prompt")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Partial prompt on EOF should also flush with MB.
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "partial prompt" {
		t.Errorf("expected 'partial prompt', got %q", value)
	}
	// MB
	tag, _, err = stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read MB TLV: %v", err)
	}
	if tag != stream.TagMessageBoundary {
		t.Errorf("expected MB tag, got %s", tag)
	}
}

func TestReadPrompts_Command(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader(":cancel\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Commands should emit UT without MB.
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != ":cancel" {
		t.Errorf("expected ':cancel', got %q", value)
	}

	// Should be no MB after command
	tag, _, err = stream.ReadTLV(&buf)
	if err == nil {
		t.Errorf("expected EOF after command, got tag %s", tag)
	}
}

func TestReadPrompts_QuitCommand(t *testing.T) {
	var buf bytes.Buffer

	// :quit should return nil immediately without any output
	input := strings.NewReader("some text\n:quit\nmore text\n")
	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only "some text" should be emitted
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "some text" {
		t.Errorf("expected 'some text', got %q", value)
	}

	// MB after first prompt
	tag, _, err = stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read MB TLV: %v", err)
	}
	if tag != stream.TagMessageBoundary {
		t.Errorf("expected MB tag, got %s", tag)
	}

	// Should be no more data
	if buf.Len() > 0 {
		t.Errorf("expected no more data after :quit, got %d bytes", buf.Len())
	}
}

func TestReadPrompts_BackslashThenEOF(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("hello\\\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The backslash consumed the newline, "hello" is the accumulated text.
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "hello" {
		t.Errorf("expected 'hello', got %q", value)
	}
	// MB
	tag, _, err = stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read MB TLV: %v", err)
	}
	if tag != stream.TagMessageBoundary {
		t.Errorf("expected MB tag, got %s", tag)
	}
}

func TestReadPrompts_ReturnsEOFError(t *testing.T) {
	var buf bytes.Buffer
	input := &errorReader{}

	err := readPrompts(&buf, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// errorReader returns an error on every read
type errorReader struct{}

func (r *errorReader) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
