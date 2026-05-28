package agent

import (
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

func TestWriteToolOutput(t *testing.T) {
	// Create a mock output to capture TLV messages
	output := &mockOutput{}
	session := &Session{
		SessionConfig: SessionConfig{Output: output},
	}

	// Test success case
	session.writeToolOutput("tool123", "output text", "success")

	// Parse the written data to extract TLV
	tag, value := parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionResult {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionResult, tag)
	}

	var got stream.ToolResultData
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "tool123" || got.Output != "output text" || got.Status != "success" {
		t.Errorf("Expected {tool123, output text, success}, got {%s, %s, %s}", got.ID, got.Output, got.Status)
	}

	// Test error case
	output.data = nil
	session.writeToolOutput("tool456", "error message", "failed")

	tag, value = parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionResult {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionResult, tag)
	}

	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "tool456" || got.Output != "error message" || got.Status != "failed" {
		t.Errorf("Expected {tool456, error message, failed}, got {%s, %s, %s}", got.ID, got.Output, got.Status)
	}
}

func TestOnToolResultCallback(t *testing.T) {
	// Create a session with mock output
	output := &mockOutput{}
	session := &Session{
		SessionConfig: SessionConfig{Output: output},
		Messages:      []llm.Message{},
	}

	// Create a mock tool result callback (simulating what happens in processPrompt)
	callback := func(toolCallID string, toolResult llm.ToolResultOutput) error { //nolint:unparam // callback signature required by interface
		// Add tool result message to session messages
		session.Messages = append(session.Messages, llm.Message{
			Role: llm.RoleTool,
			Content: []llm.ContentPart{llm.ToolResultPart{
				Type:       "tool_result",
				ToolCallID: toolCallID,
				Output:     toolResult,
			}},
		})

		// Send tool result with status to adaptor
		status := "success"
		if _, ok := toolResult.(llm.ToolResultOutputFailed); ok {
			status = "failed"
		}
		if textOutput, ok := toolResult.(llm.ToolResultOutputText); ok {
			session.writeToolOutput(toolCallID, textOutput.Text, status)
		} else if errOutput, ok := toolResult.(llm.ToolResultOutputFailed); ok {
			session.writeToolOutput(toolCallID, errOutput.Reason, status)
		}

		return nil
	}

	// Test success result
	err := callback("call1", llm.ToolResultOutputText{Type: "text", Text: "success output"})
	if err != nil {
		t.Fatalf("Callback returned error: %v", err)
	}

	// Check that message was added
	if len(session.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(session.Messages))
	}

	// Check that TLV was sent
	tag, value := parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionResult {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionResult, tag)
	}

	var got stream.ToolResultData
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "call1" || got.Status != "success" {
		t.Errorf("Expected {call1, success}, got {%s, %s}", got.ID, got.Status)
	}

	// Test error result
	output.data = nil
	err = callback("call2", llm.ToolResultOutputFailed{Type: "error", Reason: "something failed"})
	if err != nil {
		t.Fatalf("Callback returned error: %v", err)
	}

	tag, value = parseTLVFromBytes(output.data)
	if tag != stream.TagFunctionResult {
		t.Errorf("Expected tag %s, got %s", stream.TagFunctionResult, tag)
	}

	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "call2" || got.Status != "failed" {
		t.Errorf("Expected {call2, failed}, got {%s, %s}", got.ID, got.Status)
	}
}

func TestWriteToolCallWithPending(t *testing.T) {
	// Create a session with mock output
	output := &mockOutput{}
	session := &Session{
		SessionConfig: SessionConfig{Output: output},
	}

	session.writeToolCall("execute_command", `{"command":"ls"}`, "tool123")

	// Should have written one TLV message: AF with type "call"
	// Status "pending" is inferred by the terminal from window creation.

	// Parse the message (tool call display)
	tag1, value1 := parseTLVFromBytes(output.data)
	if tag1 != stream.TagFunction {
		t.Errorf("Expected tag %s, got %s", stream.TagFunction, tag1)
	}

	// The tool call should be JSON with id, type, name, input
	var fd1 stream.FunctionData
	if err := json.Unmarshal([]byte(value1), &fd1); err != nil {
		t.Fatalf("Failed to parse AF JSON: %v", err)
	}
	if fd1.Type != "call" || fd1.Name != "execute_command" {
		t.Errorf("Expected type=call, name=execute_command, got type=%s, name=%s", fd1.Type, fd1.Name)
	}
	if fd1.ID != "tool123" {
		t.Errorf("Expected id=tool123, got %s", fd1.ID)
	}
	if fd1.Input != `{"command":"ls"}` {
		t.Errorf("Expected input with command=ls, got %s", fd1.Input)
	}
}

// parseTLVFromBytes extracts tag and value from TLV-encoded bytes
func parseTLVFromBytes(data []byte) (string, string) {
	if len(data) < 6 {
		return "", ""
	}
	tag := string(data[0:2])
	length := int(binary.BigEndian.Uint32(data[2:6]))
	if len(data) < 6+length {
		return tag, ""
	}
	value := string(data[6 : 6+length])
	return tag, value
}
