package terminal

import (
	"testing"
)

func TestGenericHandler_FormatCall_WithArgs(t *testing.T) {
	h := &GenericHandler{name: "my_tool"}

	// Normal input with actual arguments should show the args
	result := h.FormatCall([]byte(`{"key":"value"}`))
	expected := "my_tool: {\"key\":\"value\"}\n"
	if result != expected {
		t.Errorf("FormatCall with args = %q, want %q", result, expected)
	}
}
