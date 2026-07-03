package agent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/protocol"
	"github.com/alayacore/alayacore/internal/tlv"
)

func TestWriteToolOutput(t *testing.T) {
	output := &mockOutput{}
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}

	session.writeToolOutput("tool123", []llm.ContentPart{&llm.TextPart{Text: "output text"}}, false)

	tag, value := parseTLVFromBytes(output.data)
	if tag != tlv.TagUserF {
		t.Errorf("Expected tag %s, got %s", tlv.TagUserF, tag)
	}

	var got protocol.ToolOutputData
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "tool123" || got.IsError != false {
		t.Errorf("Expected {tool123, false}, got {%s, %t}", got.ID, got.IsError)
	}
	// Content should contain the output text
	checkToolResultContent(t, got.Output, "output text")

	output.data = nil
	session.writeToolOutput("tool456", []llm.ContentPart{&llm.TextPart{Text: "error message"}}, true)

	tag, value = parseTLVFromBytes(output.data)
	if tag != tlv.TagUserF {
		t.Errorf("Expected tag %s, got %s", tlv.TagUserF, tag)
	}

	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "tool456" || got.IsError != true {
		t.Errorf("Expected {tool456, true}, got {%s, %t}", got.ID, got.IsError)
	}
	checkToolResultContent(t, got.Output, "error message")
}

func TestOnToolOutputCallback(t *testing.T) {
	output := &mockOutput{}
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}

	// Simulate the OnToolOutput callback from processPrompt
	callback := func(id string, content []llm.ContentPart, err error) {
		session.Contents = append(session.Contents, &llm.ToolOutputPart{
			ID:      id,
			Output:  content,
			IsError: err != nil,
		})

		if err != nil {
			session.writeToolOutput(id, content, true)
		} else {
			session.writeToolOutput(id, content, false)
		}
	}

	callback("call1", []llm.ContentPart{&llm.TextPart{Text: "success output"}}, nil)

	if len(session.Contents) != 1 {
		t.Fatalf("Expected 1 content part, got %d", len(session.Contents))
	}

	tag, value := parseTLVFromBytes(output.data)
	if tag != tlv.TagUserF {
		t.Errorf("Expected tag %s, got %s", tlv.TagUserF, tag)
	}

	var got protocol.ToolOutputData
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "call1" || got.IsError != false {
		t.Errorf("Expected {call1, false}, got {%s, %t}", got.ID, got.IsError)
	}

	output.data = nil
	callback("call2", []llm.ContentPart{&llm.TextPart{Text: "something failed"}}, errors.New("something failed"))

	tag, value = parseTLVFromBytes(output.data)
	if tag != tlv.TagUserF {
		t.Errorf("Expected tag %s, got %s", tlv.TagUserF, tag)
	}

	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("Failed to parse UF JSON: %v", err)
	}
	if got.ID != "call2" || got.IsError != true {
		t.Errorf("Expected {call2, true}, got {%s, %t}", got.ID, got.IsError)
	}
}

func TestWriteToolCallWithPending(t *testing.T) {
	output := &mockOutput{}
	session := &Session{
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{Output: output},
		},
	}

	session.writeToolInput(json.RawMessage(`{"command":"ls"}`), "tool123")

	tag1, value1 := parseTLVFromBytes(output.data)
	if tag1 != tlv.TagAssistantF {
		t.Errorf("Expected tag %s, got %s", tlv.TagAssistantF, tag1)
	}

	var fd1 protocol.ToolInputData
	if err := json.Unmarshal([]byte(value1), &fd1); err != nil {
		t.Fatalf("Failed to parse AF JSON: %v", err)
	}
	if fd1.Name != "" {
		t.Errorf("Expected empty name (input frame), got name=%s", fd1.Name)
	}
	if fd1.ID != "tool123" {
		t.Errorf("Expected id=tool123, got %s", fd1.ID)
	}
	if string(fd1.Input) != `{"command":"ls"}` {
		t.Errorf("Expected input with command=ls, got %s", fd1.Input)
	}
}

func checkToolResultContent(t *testing.T, content json.RawMessage, expectedText string) {
	t.Helper()
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(content, &items); err != nil {
		t.Fatalf("Failed to parse content: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Expected 1 content item, got %d", len(items))
	}
	if items[0].Type != "text" {
		t.Errorf("Expected type 'text', got %q", items[0].Type)
	}
	if items[0].Text != expectedText {
		t.Errorf("Expected text %q, got %q", expectedText, items[0].Text)
	}
}

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
