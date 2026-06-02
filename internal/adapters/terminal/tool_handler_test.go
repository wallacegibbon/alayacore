package terminal

import (
	"testing"
)

func TestGenericHandler_FormatCall_Placeholder(t *testing.T) {
	h := &GenericHandler{name: "my_tool"}

	// Empty JSON object (placeholder from ToolCallStart) should show just
	// the tool name as a head line, not the raw "{}".
	result := h.FormatCall([]byte("{}"), nil)
	expected := "my_tool: \n"
	if result != expected {
		t.Errorf("FormatCall with {} = %q, want %q", result, expected)
	}
}

func TestGenericHandler_FormatCall_WithArgs(t *testing.T) {
	h := &GenericHandler{name: "my_tool"}

	// Normal input with actual arguments should show the args
	result := h.FormatCall([]byte(`{"key":"value"}`), nil)
	expected := "my_tool: {\"key\":\"value\"}\n"
	if result != expected {
		t.Errorf("FormatCall with args = %q, want %q", result, expected)
	}
}
