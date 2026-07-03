package tools

import (
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()

	// Clean up temp files created by tests (truncation paths in
	// saveToTmpFile). Tests don't go through main.go's Cleanup().
	Cleanup()

	os.Exit(code)
}

func TestDefaultToolsAll(t *testing.T) {
	tools, err := DefaultTools(ToolFilter{AllBuiltins: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != len(BuiltinTools) {
		t.Errorf("expected %d tools, got %d", len(BuiltinTools), len(tools))
	}
}

func TestDefaultToolsNone(t *testing.T) {
	tools, err := DefaultTools(ToolFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestDefaultToolsFilter(t *testing.T) {
	tools, err := DefaultTools(ToolFilter{Selected: []string{"read_file", "write_file"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

func TestDefaultToolsUnknown(t *testing.T) {
	_, err := DefaultTools(ToolFilter{Selected: []string{"read_file", "blah"}})
	if err == nil {
		t.Fatal("expected error for unknown tool name")
	}
	if !strings.Contains(err.Error(), "blah") {
		t.Errorf("expected error to mention 'blah', got: %v", err)
	}
	if !strings.Contains(err.Error(), "unknown built-in tool") {
		t.Errorf("expected error to mention 'unknown built-in tool', got: %v", err)
	}
}
