package terminal

import (
	"testing"

	"github.com/alayacore/alayacore/internal/stream"
)

func TestUpdateToolStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window
	wb.AppendOrUpdate("tool123", stream.TagFunctionNotify, "posix_shell: git status")

	// Verify window was created
	if len(wb.Windows) != 1 {
		t.Fatalf("Expected 1 window, got %d", len(wb.Windows))
	}

	// Initially no status
	if wb.Windows[0].Status != "" {
		t.Errorf("Expected empty status, got %s", wb.Windows[0].Status)
	}

	// Update with pending status
	wb.UpdateToolStatus("tool123", "pending")

	// Check status was updated
	if wb.Windows[0].Status != "pending" {
		t.Errorf("Expected status 'pending', got %s", wb.Windows[0].Status)
	}

	// Update with success status
	wb.UpdateToolStatus("tool123", "success")

	// Check status was updated
	if wb.Windows[0].Status != "success" {
		t.Errorf("Expected status 'success', got %s", wb.Windows[0].Status)
	}

	// Update with error status
	wb.UpdateToolStatus("tool123", "error")

	// Check status was updated
	if wb.Windows[0].Status != "error" {
		t.Errorf("Expected status 'error', got %s", wb.Windows[0].Status)
	}

	// Try to update non-existent window (should not crash)
	wb.UpdateToolStatus("nonexistent", "success")
}

func TestRenderWindowContentWithStatus(t *testing.T) {
	wb := NewWindowBuffer(80, DefaultStyles())

	// Create a tool window
	wb.AppendOrUpdate("tool123", stream.TagFunctionNotify, "posix_shell: git status")

	// Test rendering without status (default for loaded sessions)
	content := wb.renderWindowContent(wb.Windows[0], 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed hollow dot (·) as default for tool windows without status
	if !contains(content, "·") {
		t.Errorf("Expected content to contain hollow dot (·), got: %s", content)
	}

	// Update with pending status
	wb.UpdateToolStatus("tool123", "pending")

	// Test rendering with pending status
	content = wb.renderWindowContent(wb.Windows[0], 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain dimmed filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Update with success status
	wb.UpdateToolStatus("tool123", "success")

	// Test rendering with success status
	content = wb.renderWindowContent(wb.Windows[0], 76)
	if content == "" {
		t.Error("Expected non-empty content")
	}
	// Should contain filled dot (•)
	if !contains(content, "•") {
		t.Errorf("Expected content to contain filled dot (•), got: %s", content)
	}

	// Update with error status
	wb.UpdateToolStatus("tool123", "error")

	// Test rendering with error status
	content = wb.renderWindowContent(wb.Windows[0], 76)
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
