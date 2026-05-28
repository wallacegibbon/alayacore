package terminal

import (
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
)

func TestHandleFunctionEvent(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Send a "call" type event (creates the window)
	wb.HandleFunctionEvent(stream.FunctionData{
		ID:    "tool123",
		Type:  "call",
		Name:  "execute_command",
		Input: "execute_command: git status",
	})

	// Verify window was created
	if wb.GetWindowCount() != 1 {
		t.Fatalf("Expected 1 window, got %d", wb.GetWindowCount())
	}

	// Status defaults to pending (inferred from window creation)
	if wb.GetWindow(0).Status != ToolStatusPending {
		t.Errorf("Expected ToolStatusPending, got %v", wb.GetWindow(0).Status)
	}

	// Send a result
	wb.HandleFunctionResult("tool123", "output text", "success")

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusSuccess {
		t.Errorf("Expected ToolStatusSuccess, got %v", wb.GetWindow(0).Status)
	}

	// Send a result with error
	wb.HandleFunctionResult("tool123", "error output", "failed")

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusError {
		t.Errorf("Expected ToolStatusError, got %v", wb.GetWindow(0).Status)
	}

	// Try to update non-existent window (should not crash)
	wb.HandleFunctionResult("nonexistent", "output", "success")
}

func TestRenderWindowContentWithStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window
	wb.HandleFunctionEvent(stream.FunctionData{
		ID:    "tool123",
		Type:  "call",
		Name:  "execute_command",
		Input: "execute_command: git status",
	})

	// Test rendering with pending status (default on creation)
	w := wb.GetWindow(0)
	content := wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed filled dot (•) for pending status
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Send result with success
	wb.HandleFunctionResult("tool123", "output", "success")

	// Test rendering with success status
	content = wb.RenderWindowContent(w, 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Send result with error
	wb.HandleFunctionResult("tool123", "error output", "failed")

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
	// End-to-end test: write TagFunction TLVs through the actual
	// outputWriter pipeline (Write → processBuffer → writeColored → HandleFunctionEvent).
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	makeFD := func(id, typ, name, input string) []byte {
		fd, _ := json.Marshal(stream.FunctionData{
			ID:    id,
			Type:  typ,
			Name:  name,
			Input: input,
		})
		return stream.EncodeTLV(stream.TagFunction, string(fd))
	}

	// 1. Simulate ToolCallStart: type "start" with placeholder JSON input
	_, err := out.Write(makeFD("call-abc", "start", "write_file", "{}"))
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

	// 2. Simulate ToolCallComplete: type "call" with full JSON input
	_, err = out.Write(makeFD("call-abc", "call", "write_file", `{"path":"/tmp/f.txt","content":"hello world"}`))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if wb.GetWindowCount() != 1 {
		t.Fatalf("Expected still 1 window, got %d", wb.GetWindowCount())
	}
	w = wb.GetWindow(0)
	if w.ToolInput != "write_file: /tmp/f.txt\nhello world" {
		t.Errorf("Expected full input, got %q", w.ToolInput)
	}
}
