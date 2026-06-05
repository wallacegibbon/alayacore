package terminal

import (
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
	"github.com/alayacore/alayacore/internal/theme"
)

func TestHandleToolUseEvent(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Send a "call" type event (creates the window with Name set = start frame)
	wb.HandleToolUseEvent(stream.ToolUseData{
		ID:    "tool123",
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
	wb.HandleToolResult("tool123", "output text", false)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusSuccess {
		t.Errorf("Expected ToolStatusSuccess, got %v", wb.GetWindow(0).Status)
	}

	// Send a result with error
	wb.HandleToolResult("tool123", "error output", true)

	// Check status was updated
	if wb.GetWindow(0).Status != ToolStatusError {
		t.Errorf("Expected ToolStatusError, got %v", wb.GetWindow(0).Status)
	}

	// Try to update non-existent window (should not crash)
	wb.HandleToolResult("nonexistent", "output", false)
}

func TestRenderWindowContentWithStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window (Name set = start frame)
	wb.HandleToolUseEvent(stream.ToolUseData{
		ID:    "tool123",
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
	wb.HandleToolResult("tool123", "output", false)

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
	wb.HandleToolResult("tool123", "error output", true)

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
	// End-to-end test: write TagAssistantF TLVs through the actual
	// outputWriter pipeline (Write → processBuffer → writeColored → HandleToolUseEvent).
	out := NewTerminalOutput(NewStyles(theme.DefaultTheme()))
	out.SetWindowWidth(80)

	makeStartFD := func(id, name string) []byte {
		fd, _ := json.Marshal(stream.ToolUseData{
			ID:   id,
			Name: name,
		})
		return stream.EncodeTLV(stream.TagAssistantF, string(fd))
	}

	makeInputFD := func(id, input string) []byte {
		fd, _ := json.Marshal(stream.ToolUseData{
			ID:    id,
			Input: input,
		})
		return stream.EncodeTLV(stream.TagAssistantF, string(fd))
	}

	// 1. Simulate ToolCallStart: Name set, no input yet
	_, err := out.Write(makeStartFD("call-abc", "write_file"))
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

	// 2. Simulate ToolCallComplete: Name nil with full JSON input
	_, err = out.Write(makeInputFD("call-abc", `{"path":"/tmp/f.txt","content":"hello world"}`))
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
