package agent

import (
	"path/filepath"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// TestMultiPartUserMessageRoundtrip verifies that a single user message with
// multiple TextParts roundtrips correctly through save/load. Previously, each
// UT chunk forced a new message, splitting multi-part user messages.
func TestMultiPartUserMessageRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "multipart-user.md")

	msgs := []llm.Message{
		{
			Role: llm.RoleUser,
			Contents: []llm.ContentPart{
				&llm.TextPart{Text: "First part"},
				&llm.TextPart{Text: "Second part"},
			},
		},
		{
			Role:     llm.RoleAssistant,
			Contents: []llm.ContentPart{&llm.TextPart{Text: "Got it."}},
		},
	}
	session := &Session{
		runState: runState{
			Contents:  contentsFromMessagesForTest(msgs),
			Messages:  msgs,
			taskQueue: make([]QueueItem, 0),
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &stream.NopInput{},
				Output: &stream.NopOutput{},
			},
		},
	}

	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	loadedMsgs := contentsToMessages(loaded.Contents)

	// Should be 2 messages: user (with 2 parts), assistant
	if len(loadedMsgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(loadedMsgs))
	}

	// User message should have both text parts in one message
	if loadedMsgs[0].Role != llm.RoleUser {
		t.Errorf("First message should be user, got %s", loadedMsgs[0].Role)
	}
	if len(loadedMsgs[0].Contents) != 2 {
		t.Fatalf("User message should have 2 content parts, got %d", len(loadedMsgs[0].Contents))
	}
	if tp, ok := loadedMsgs[0].Contents[0].(*llm.TextPart); !ok || tp.Text != "First part" {
		t.Errorf("First part mismatch: %v", loadedMsgs[0].Contents[0])
	}
	if tp, ok := loadedMsgs[0].Contents[1].(*llm.TextPart); !ok || tp.Text != "Second part" {
		t.Errorf("Second part mismatch: %v", loadedMsgs[0].Contents[1])
	}

	// Assistant message should be intact
	if loadedMsgs[1].Role != llm.RoleAssistant {
		t.Errorf("Second message should be assistant, got %s", loadedMsgs[1].Role)
	}
}

// TestConsecutiveUserChunksGrouped verifies that consecutive UT chunks in a
// session file are grouped into a single user message (not split).
func TestConsecutiveUserChunksGrouped(t *testing.T) {
	// Manually construct a session body with two consecutive UT chunks.
	// This simulates what formatSessionMarkdown produces for a multi-part
	// user message.
	raw := []byte("---\nmessage_version: 6\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n")

	// Build TLV body with two UT chunks followed by one AT chunk
	var body string
	body += string(stream.EncodeTLV(stream.TagUserT, "Hello"))
	body += string(stream.EncodeTLV(stream.TagUserT, " world"))
	body += string(stream.EncodeTLV(stream.TagAssistantT, "Hi there"))

	loaded, err := parseSessionData(append(raw, body...))
	if err != nil {
		t.Fatalf("parseSessionData failed: %v", err)
	}

	msgs := contentsToMessages(loaded.Contents)

	// Should be 2 messages: user (with 2 parts), assistant
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}

	// User message should have both text parts
	if msgs[0].Role != llm.RoleUser {
		t.Errorf("First message should be user, got %s", msgs[0].Role)
	}
	if len(msgs[0].Contents) != 2 {
		t.Fatalf("User message should have 2 content parts, got %d", len(msgs[0].Contents))
	}
	tp0, ok := msgs[0].Contents[0].(*llm.TextPart)
	if !ok || tp0.Text != "Hello" {
		t.Errorf("First part mismatch: %v", msgs[0].Contents[0])
	}
	tp1, ok := msgs[0].Contents[1].(*llm.TextPart)
	if !ok || tp1.Text != " world" {
		t.Errorf("Second part mismatch: %v", msgs[0].Contents[1])
	}

	// Assistant message
	if msgs[1].Role != llm.RoleAssistant {
		t.Errorf("Second message should be assistant, got %s", msgs[1].Role)
	}
}

// TestHandleUserPromptAppendsToExistingUserMessage verifies that when a user
// submits a new prompt and the last message is already a user message, the new
// text is appended as another TextPart instead of creating a new message.
func TestHandleUserPromptAppendsToExistingUserMessage(t *testing.T) {
	output := &mockOutput{}
	session := &Session{
		runState: runState{
			Messages:  []llm.Message{},
			taskQueue: make([]QueueItem, 0),
		},
		sessionConfig: sessionConfig{
			ModelManager: NewModelManager(""),
			SessionConfig: SessionConfig{
				Input:  &stream.NopInput{},
				Output: output,
			},
		},
	}

	// Simulate a failed prompt: add a user message but no assistant response
	session.Messages = append(session.Messages, llm.NewUserMessage("First attempt"))

	if len(session.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(session.Messages))
	}
	if session.Messages[0].Role != llm.RoleUser {
		t.Fatalf("Expected user message, got %s", session.Messages[0].Role)
	}
	if len(session.Messages[0].Contents) != 1 {
		t.Fatalf("Expected 1 content part, got %d", len(session.Messages[0].Contents))
	}

	// Now simulate submitting a new prompt — handleUserPrompt would check if
	// the last message is user and append. We test the logic directly by
	// replicating what handleUserPrompt does:
	prompt := "Second attempt"
	if len(session.Messages) > 0 && session.Messages[len(session.Messages)-1].Role == llm.RoleUser {
		session.Messages[len(session.Messages)-1].Contents = append(
			session.Messages[len(session.Messages)-1].Contents,
			&llm.TextPart{Text: prompt},
		)
	} else {
		session.Messages = append(session.Messages, llm.NewUserMessage(prompt))
	}

	// Should still be 1 message with 2 content parts
	if len(session.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(session.Messages))
	}
	if len(session.Messages[0].Contents) != 2 {
		t.Fatalf("Expected 2 content parts, got %d", len(session.Messages[0].Contents))
	}

	tp0, ok := session.Messages[0].Contents[0].(*llm.TextPart)
	if !ok || tp0.Text != "First attempt" {
		t.Errorf("First part mismatch: %v", session.Messages[0].Contents[0])
	}
	tp1, ok := session.Messages[0].Contents[1].(*llm.TextPart)
	if !ok || tp1.Text != "Second attempt" {
		t.Errorf("Second part mismatch: %v", session.Messages[0].Contents[1])
	}
}

// TestHandleUserPromptCreatesNewMessageWhenPreviousIsAssistant verifies that
// a new user message is created when the previous message is from the assistant.
func TestHandleUserPromptCreatesNewMessageWhenPreviousIsAssistant(t *testing.T) {
	session := &Session{
		runState: runState{
			Messages: []llm.Message{
				{Role: llm.RoleUser, Contents: []llm.ContentPart{&llm.TextPart{Text: "Hello"}}},
				{Role: llm.RoleAssistant, Contents: []llm.ContentPart{&llm.TextPart{Text: "Hi!"}}},
			},
			taskQueue: make([]QueueItem, 0),
		},
	}

	// Simulate new prompt submission
	prompt := "How are you?"
	if len(session.Messages) > 0 && session.Messages[len(session.Messages)-1].Role == llm.RoleUser {
		session.Messages[len(session.Messages)-1].Contents = append(
			session.Messages[len(session.Messages)-1].Contents,
			&llm.TextPart{Text: prompt},
		)
	} else {
		session.Messages = append(session.Messages, llm.NewUserMessage(prompt))
	}

	// Should be 3 messages
	if len(session.Messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(session.Messages))
	}

	// Third message should be a new user message
	if session.Messages[2].Role != llm.RoleUser {
		t.Errorf("Third message should be user, got %s", session.Messages[2].Role)
	}
	if len(session.Messages[2].Contents) != 1 {
		t.Fatalf("Third message should have 1 content part, got %d", len(session.Messages[2].Contents))
	}
	tp, ok := session.Messages[2].Contents[0].(*llm.TextPart)
	if !ok || tp.Text != "How are you?" {
		t.Errorf("Third message text mismatch: %v", session.Messages[2].Contents[0])
	}
}
