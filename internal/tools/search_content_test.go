package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestRGAvailable(t *testing.T) {
	// This just tests that RGAvailable doesn't panic
	_ = RGAvailable()
}

func TestSearchContentBasicSearch(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	// Create a temp directory with some test files
	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "hello world\nfoo bar\nhello again\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Search for "hello"
	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "hello",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text == "" {
		t.Error("expected non-empty output")
	}
}

func TestSearchContentNoMatches(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "nonexistent_pattern_xyz",
		Path:    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}
	if text.Text != "No matches found" {
		t.Errorf("expected 'No matches found', got %q", text.Text)
	}
}

func TestSearchContentEmptyPattern(t *testing.T) {
	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern: "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errOut, ok := result.(llm.ToolResultOutputError)
	if !ok {
		t.Fatalf("expected error output, got %T", result)
	}
	if errOut.Error != "pattern is required" {
		t.Errorf("expected 'pattern is required', got %q", errOut.Error)
	}
}

func TestSearchContentFileTypeFilter(t *testing.T) {
	if !RGAvailable() {
		t.Skip("rg not available on system")
	}

	tmpDir, err := os.MkdirTemp("", "alayacore-rg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a Go file
	goFile := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc test() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a text file that also contains "func"
	txtFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(txtFile, []byte("func should not match\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := executeSearchContent(context.Background(), SearchContentInput{
		Pattern:  "func",
		Path:     tmpDir,
		FileType: "go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text, ok := result.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected text output, got %T", result)
	}

	// Should only contain the Go file match
	if text.Text == "" {
		t.Error("expected non-empty output")
	}
}
