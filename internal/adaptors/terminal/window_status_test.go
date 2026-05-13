package terminal

import (
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestUpdateToolStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window
	wb.AppendToolCall("tool123", "execute_command", "execute_command: git status")

	// Verify window was created
	if wb.GetWindowCount() != 1 {
		t.Fatalf("Expected 1 window, got %d", wb.GetWindowCount())
	}

	// Initially no status
	if wb.GetWindow(0).Status != ToolStatusNone {
		t.Errorf("Expected ToolStatusNone, got %v", wb.GetWindow(0).Status)
	}

	// Update with pending status
	wb.UpdateToolStatus("tool123", ToolStatusPending)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusPending {
		t.Errorf("Expected ToolStatusPending, got %v", wb.GetWindow(0).Status)
	}

	// Update with success status
	wb.UpdateToolStatus("tool123", ToolStatusSuccess)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusSuccess {
		t.Errorf("Expected ToolStatusSuccess, got %v", wb.GetWindow(0).Status)
	}

	// Update with error status
	wb.UpdateToolStatus("tool123", ToolStatusError)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusError {
		t.Errorf("Expected ToolStatusError, got %v", wb.GetWindow(0).Status)
	}

	// Try to update non-existent window (should not crash)
	wb.UpdateToolStatus("nonexistent", ToolStatusSuccess)
}

func TestRenderWindowContentWithStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window
	wb.AppendToolCall("tool123", "execute_command", "execute_command: git status")

	// Test rendering without status (default for loaded sessions)
	w := wb.GetWindow(0)
	content := wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed hollow dot (·) as default for tool windows without status
	if !contains(content, "·") {
		t.Errorf("Expected content to contain hollow dot (·), got: %s", content)
	}

	// Update with pending status
	wb.UpdateToolStatus("tool123", ToolStatusPending)

	// Test rendering with pending status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Update with success status
	wb.UpdateToolStatus("tool123", ToolStatusSuccess)

	// Test rendering with success status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Update with error status
	wb.UpdateToolStatus("tool123", ToolStatusError)

	// Test rendering with error status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestOutputWriterToolCallStartThenFull(t *testing.T) {
	// End-to-end test: write TagFunctionCall TLVs through the actual
	// outputWriter pipeline (Write → processBuffer → writeColored → AppendToolCall).
	out := NewTerminalOutput(NewStyles(DefaultTheme()))
	out.SetWindowWidth(80)

	makeTLV := func(id, name, input string) []byte {
		tc, _ := json.Marshal(stream.ToolCallData{ID: id, Name: name, Input: input})
		return stream.EncodeTLV(stream.TagFunctionCall, string(tc))
	}

	// 1. Simulate ToolCallStart: placeholder with "{}" input
	_, err := out.Write(makeTLV("call-abc", "write_file", "{}"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	wb := out.WindowBuffer()
	if wb.GetWindowCount() != 1 {
		t.Fatalf("Expected 1 window after start event, got %d", wb.GetWindowCount())
	}
	w := wb.GetWindow(0)
	if w.ToolName != "write_file" {
		t.Errorf("Expected tool name 'write_file', got %q", w.ToolName)
	}
	// The placeholder should show "write_file: " (empty path, no content)
	if w.Content == "" {
		t.Error("Expected non-empty placeholder content")
	}

	// 2. Simulate ToolCallComplete: full input replaces placeholder
	_, err = out.Write(makeTLV("call-abc", "write_file", `{"path":"/tmp/f.txt","content":"hello world"}`))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if wb.GetWindowCount() != 1 {
		t.Fatalf("Expected still 1 window, got %d", wb.GetWindowCount())
	}
	w = wb.GetWindow(0)
	if w.Content != "write_file: /tmp/f.txt\nhello world" {
		t.Errorf("Expected full content, got %q", w.Content)
	}
}
