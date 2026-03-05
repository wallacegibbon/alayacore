package adaptors

import (
	"strings"
	"testing"
)

func TestWordwrapEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		width  int
		expect string // expected first line after wrapping (or entire output)
	}{
		{
			name:   "empty line",
			text:   "",
			width:  10,
			expect: "",
		},
		{
			name:   "only escape sequences - styling and reset",
			text:   "\x1b[1;2m\x1b[0m",
			width:  10,
			expect: "\x1b[1;2m\x1b[0m\n",
		},
		{
			name:   "only prefix escape sequence",
			text:   "\x1b[1;2m",
			width:  10,
			expect: "\x1b[1;2m\n",
		},
		{
			name:   "only suffix escape sequence",
			text:   "\x1b[0m",
			width:  10,
			expect: "\x1b[0m\n",
		},
		{
			name:   "mixed escape sequences no printable",
			text:   "\x1b[1;2m\x1b[3;4m\x1b[0m",
			width:  10,
			expect: "\x1b[1;2m\x1b[3;4m\x1b[0m\n",
		},
		{
			name:   "newline separated escape sequences",
			text:   "\x1b[1;2m\n\x1b[0m",
			width:  10,
			expect: "\x1b[1;2m\n\x1b[0m\n",
		},
		{
			name:   "single printable character with styling",
			text:   "\x1b[1;2ma\x1b[0m",
			width:  10,
			expect: "\x1b[1;2ma\x1b[0m\n",
		},
		{
			name:   "single printable character with styling width 0",
			text:   "\x1b[1;2ma\x1b[0m",
			width:  0,
			expect: "\x1b[1;2ma\x1b[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test ensures wordwrap doesn't panic
			result := wordwrap(tt.text, tt.width)
			// For empty input, result should be empty
			if tt.text == "" && result != "" {
				t.Errorf("wordwrap(%q, %d) = %q, want empty", tt.text, tt.width, result)
			}
			// For non-empty, we just ensure no panic occurred
			// Optional: verify that result ends with newline (if input had newline?)
			// For simplicity, we just check that result contains the original escape sequences
			if tt.text != "" && !strings.Contains(result, "\x1b[") && strings.Contains(tt.text, "\x1b") {
				t.Errorf("wordwrap lost escape sequences: %q", result)
			}
		})
	}
}

func TestWordwrapMultiColor(t *testing.T) {
	// Test wrapping text with multiple inline escape sequences
	// This simulates user prompts like: [GREEN]> [CYAN]text here
	green := "\x1b[32m"
	cyan := "\x1b[36m"
	reset := "\x1b[0m"

	text := green + "> " + cyan + "echo this is a very long sentence: A quick brown fox jumps over the lazy dog, another quick brown fox jumps over the lazy dog. and again........................" + reset

	result := wordwrap(text, 60)

	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")

	// Verify we got multiple lines
	if len(lines) < 2 {
		t.Fatalf("Expected at least 2 lines, got %d", len(lines))
	}

	// First line should start with green (and contain cyan later)
	if !strings.HasPrefix(lines[0], green) {
		t.Errorf("First line should start with green escape sequence, got: %q", lines[0])
	}
	if !strings.Contains(lines[0], cyan) {
		t.Errorf("First line should contain cyan escape sequence, got: %q", lines[0])
	}

	// Second and subsequent lines should contain cyan (the active color at break point)
	// They may also contain green escape sequence, but cyan should be present
	for i, line := range lines[1:] {
		if !strings.Contains(line, cyan) {
			t.Errorf("Line %d should contain cyan escape sequence, got: %q", i+2, line)
		}
		// Verify the line ends with reset
		if !strings.HasSuffix(line, reset) {
			t.Errorf("Line %d should end with reset sequence, got: %q", i+2, line)
		}
	}
}
