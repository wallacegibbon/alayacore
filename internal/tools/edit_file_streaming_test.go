package tools

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEditFileStreamingMemory(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large_test.txt")

	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	targetSize := 10 * 1024 * 1024
	chunk := make([]byte, 1024*1024)
	for i := range chunk {
		chunk[i] = byte('A' + (i % 26))
	}

	pattern := []byte("UNIQUE_PATTERN_TO_REPLACE")
	patternPos := targetSize / 2

	written := 0
	for written < targetSize {
		toWrite := len(chunk)
		if written+toWrite > targetSize {
			toWrite = targetSize - written
		}

		if written <= patternPos && written+toWrite > patternPos {
			part1 := patternPos - written
			if part1 > 0 {
				if _, wErr := file.Write(chunk[:part1]); wErr != nil {
					t.Fatalf("Failed to write: %v", wErr)
				}
				written += part1
			}
			if _, wErr := file.Write(pattern); wErr != nil {
				t.Fatalf("Failed to write pattern: %v", wErr)
			}
			written += len(pattern)
			remaining := toWrite - part1
			if remaining > 0 {
				if _, wErr := file.Write(chunk[:remaining]); wErr != nil {
					t.Fatalf("Failed to write: %v", wErr)
				}
				written += remaining
			}
		} else {
			if _, wErr := file.Write(chunk[:toWrite]); wErr != nil {
				t.Fatalf("Failed to write: %v", wErr)
			}
			written += toWrite
		}
	}
	file.Close()

	var m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	_, err = executeEditFile(context.Background(), EditFileInput{
		Path:      testFile,
		OldString: string(pattern),
		NewString: "REPLACED_SUCCESSFULLY",
	})

	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	if err != nil {
		t.Fatalf("executeEditFile failed: %v", err)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}

	if !bytes.Contains(content, []byte("REPLACED_SUCCESSFULLY")) {
		t.Error("Replacement text not found in file")
	}

	if bytes.Contains(content, pattern) {
		t.Error("Original pattern still exists in file")
	}

	memIncrease := int64(m2.Alloc) - int64(m1.Alloc)
	maxAllowed := int64(20 * 1024 * 1024)

	if memIncrease > 0 {
		t.Logf("Memory increase: %.2f MB", float64(memIncrease)/1024/1024)
		if memIncrease > maxAllowed {
			t.Errorf("Memory usage too high: %.2f MB (max allowed: %.2f MB)",
				float64(memIncrease)/1024/1024,
				float64(maxAllowed)/1024/1024)
		}
	} else {
		t.Logf("Memory decreased (GC ran): %.2f MB", float64(-memIncrease)/1024/1024)
	}
}

func TestEditFileStreamingEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		oldString   string
		newString   string
		shouldError bool
		errorMsg    string
	}{
		{
			name:        "old_string not found",
			content:     "hello world",
			oldString:   "not found",
			newString:   "replacement",
			shouldError: true,
			errorMsg:    "not found",
		},
		{
			name:        "old_string appears multiple times",
			content:     "foo bar foo",
			oldString:   "foo",
			newString:   "baz",
			shouldError: true,
			errorMsg:    "found multiple times",
		},
		{
			name:        "successful replacement",
			content:     "hello world",
			oldString:   "world",
			newString:   "universe",
			shouldError: false,
		},
		{
			name:        "empty new_string (deletion)",
			content:     "hello world",
			oldString:   " world",
			newString:   "",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			_, err := executeEditFile(context.Background(), EditFileInput{
				Path:      testFile,
				OldString: tt.oldString,
				NewString: tt.newString,
			})

			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error, got success")
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Error message should contain %q, got: %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected success, got error: %v", err)
				}

				content, err := os.ReadFile(testFile)
				if err != nil {
					t.Fatalf("Failed to read file: %v", err)
				}

				expectedContent := tt.content[:len(tt.content)-len(tt.oldString)]
				expectedContent = expectedContent[:strings.LastIndex(tt.content, tt.oldString)]
				expectedContent += tt.newString
				expectedContent += tt.content[strings.LastIndex(tt.content, tt.oldString)+len(tt.oldString):]

				if string(content) != expectedContent {
					t.Errorf("Expected content %q, got %q", expectedContent, string(content))
				}
			}
		})
	}
}
