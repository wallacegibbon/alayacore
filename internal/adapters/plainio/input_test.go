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

	// Should emit TLV(UT, "hello")
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
}

func TestReadPrompts_MultiLineBackslash(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("first line\\\nsecond line\\\nthird line\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}

	expected := "first line\nsecond line\nthird line"
	if value != expected {
		t.Errorf("expected %q, got %q", expected, value)
	}
}

func TestReadPrompts_MultiplePrompts(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("prompt1\nprompt2\nprompt3\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, expected := range []string{"prompt1", "prompt2", "prompt3"} {
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
	}
}

func TestReadPrompts_EmptyLines(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("\n\nhello\n\nworld\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty lines should be skipped
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
	}

	// No more messages
	_, _, err = stream.ReadTLV(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestReadPrompts_QuitCommand(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader(":quit\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// :quit should NOT emit a TLV message — it's handled locally
	_, _, err = stream.ReadTLV(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF (no TLV emitted for :quit), got %v", err)
	}
}

func TestReadPrompts_QuitShort(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader(":q\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = stream.ReadTLV(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF (no TLV emitted for :q), got %v", err)
	}
}

func TestReadPrompts_EOFWithPartialPrompt(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("partial prompt without newline")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("failed to read TLV: %v", err)
	}
	if tag != stream.TagUserT {
		t.Errorf("expected tag UT, got %s", tag)
	}
	if value != "partial prompt without newline" {
		t.Errorf("expected partial prompt text, got %q", value)
	}
}

func TestReadPrompts_EOFWithNoInput(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = stream.ReadTLV(&buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestReadPrompts_MixedBackslashAndNormal(t *testing.T) {
	var buf bytes.Buffer
	input := strings.NewReader("normal\nbackslash\\\ncontinuation\n")

	err := readPrompts(&buf, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First: "normal"
	tag, value, err := stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("first prompt: failed to read TLV: %v", err)
	}
	if value != "normal" {
		t.Errorf("expected 'normal', got %q", value)
	}
	_ = tag

	// Second: "backslash\ncontinuation"
	_, value, err = stream.ReadTLV(&buf)
	if err != nil {
		t.Fatalf("second prompt: failed to read TLV: %v", err)
	}
	if value != "backslash\ncontinuation" {
		t.Errorf("expected 'backslash\\ncontinuation', got %q", value)
	}
}

func TestReadPrompts_WriteError(t *testing.T) {
	// A writer that always returns an error on Write.
	failWriter := &errWriter{err: io.ErrClosedPipe}
	input := strings.NewReader("hello\n")

	err := readPrompts(failWriter, input)
	if err == nil {
		t.Fatal("expected error from write failure, got nil")
	}
	if err != io.ErrClosedPipe {
		t.Errorf("expected io.ErrClosedPipe, got %v", err)
	}
}

// errWriter returns err on every Write call.
type errWriter struct {
	err error
}

func (w *errWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}
