package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestEditFileStreamingMemory(t *testing.T) {
	// Create a moderately large test file (10MB)
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large_test.txt")

	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Write 10MB of data with a unique pattern in the middle
	targetSize := 10 * 1024 * 1024   // 10MB
	chunk := make([]byte, 1024*1024) // 1MB chunk
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
			// Write first part
			part1 := patternPos - written
			if part1 > 0 {
				if _, err := file.Write(chunk[:part1]); err != nil {
					t.Fatalf("Failed to write: %v", err)
				}
				written += part1
			}
			// Write pattern
			if _, err := file.Write(pattern); err != nil {
				t.Fatalf("Failed to write pattern: %v", err)
			}
			written += len(pattern)
			// Write remaining part
			remaining := toWrite - part1
			if remaining > 0 {
				if _, err := file.Write(chunk[:remaining]); err != nil {
					t.Fatalf("Failed to write: %v", err)
				}
				written += remaining
			}
		} else {
			if _, err := file.Write(chunk[:toWrite]); err != nil {
				t.Fatalf("Failed to write: %v", err)
			}
			written += toWrite
		}
	}

	file.Close()

	// Get memory stats before
	var m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	// Test the edit
	result, err := executeEditFile(nil, EditFileInput{
		Path:      testFile,
		OldString: string(pattern),
		NewString: "REPLACED_SUCCESSFULLY",
	})

	// Get memory stats after
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	if err != nil {
		t.Fatalf("executeEditFile failed: %v", err)
	}

	// Check if result is an error
	if errResult, ok := result.(llm.ToolResultOutputError); ok {
		t.Fatalf("Edit failed: %s", errResult.Error)
	}

	// Verify the replacement
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}

	if !contains(content, []byte("REPLACED_SUCCESSFULLY")) {
		t.Error("Replacement text not found in file")
	}

	if contains(content, pattern) {
		t.Error("Original pattern still exists in file")
	}

	// Check memory usage - should not exceed 20MB for a 10MB file
	memIncrease := int64(m2.Alloc) - int64(m1.Alloc)
	maxAllowed := int64(20 * 1024 * 1024) // 20MB

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

func contains(data []byte, substr []byte) bool {
	return len(data) >= len(substr) &&
		(len(data) == 0 && len(substr) == 0) ||
		(len(data) > 0 && findBytes(data, substr))
}

func findBytes(data, substr []byte) bool {
	for i := 0; i <= len(data)-len(substr); i++ {
		if matchBytes(data[i:], substr) {
			return true
		}
	}
	return false
}

func matchBytes(data, substr []byte) bool {
	for i, b := range substr {
		if data[i] != b {
			return false
		}
	}
	return true
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
			// Create temp file
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "test.txt")
			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Execute edit
			result, err := executeEditFile(nil, EditFileInput{
				Path:      testFile,
				OldString: tt.oldString,
				NewString: tt.newString,
			})

			if err != nil {
				t.Fatalf("executeEditFile returned error: %v", err)
			}

			// Check result
			if tt.shouldError {
				if _, ok := result.(llm.ToolResultOutputError); !ok {
					t.Errorf("Expected error result, got: %v", result)
				}
				errResult := result.(llm.ToolResultOutputError)
				if tt.errorMsg != "" && !contains([]byte(errResult.Error), []byte(tt.errorMsg)) {
					t.Errorf("Error message should contain %q, got: %q", tt.errorMsg, errResult.Error)
				}
			} else {
				if errResult, ok := result.(llm.ToolResultOutputError); ok {
					t.Errorf("Expected success, got error: %s", errResult.Error)
				}

				// Verify file content
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

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (len(s) == 0 && len(substr) == 0 || strings.Contains(s, substr))
}
