package stream

import (
	"testing"
)

func TestWrapUnwrapDelta(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		content string
	}{
		{"normal text", "1", "Hello world"},
		{"empty content", "2", ""},
		{"content after id", "1", "something after the NUL"},
		{"more content after id", "3", "more content"},
		{"unicode content", "4", "你好世界 🌍"},
		{"special chars", "5", "tabs\there\nnewlines\nand \"quotes\""},
		{"empty id would fail", "", "content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := WrapDelta(tt.id, tt.content)
			gotID, gotContent, ok := UnwrapDelta(wrapped)

			if tt.id == "" {
				if ok {
					t.Error("expected ok=false for empty id")
				}
				return
			}

			if !ok {
				t.Fatalf("UnwrapDelta returned ok=false for %q", wrapped)
			}
			if gotID != tt.id {
				t.Errorf("id = %q, want %q", gotID, tt.id)
			}
			if gotContent != tt.content {
				t.Errorf("content = %q, want %q", gotContent, tt.content)
			}
		})
	}
}

func TestUnwrapDelta_InvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"no NUL prefix", "plain text"},
		{"empty string", ""},
		{"only opening NUL", "\x00id"},
		{"only closing NUL", "id\x00content"},
		{"empty id", "\x00\x00content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := UnwrapDelta(tt.value)
			if ok {
				t.Errorf("expected ok=false for %q", tt.value)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	// Simulate the full session → adapter round trip
	id := "42"
	delta := "some thinking content"
	wrapped := WrapDelta(id, delta)

	gotID, gotContent, ok := UnwrapDelta(wrapped)
	if !ok {
		t.Fatal("UnwrapDelta failed")
	}
	if gotID != id {
		t.Errorf("id = %q, want %q", gotID, id)
	}
	if gotContent != delta {
		t.Errorf("content = %q, want %q", gotContent, delta)
	}
}
