package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// MockOutput captures output messages for testing
type MockOutput struct {
	Messages []string
}

func (m *MockOutput) Write(p []byte) (int, error) {
	m.Messages = append(m.Messages, string(p))
	return len(p), nil
}

func TestSaveAndLoadSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-session.md")

	// Create a minimal session for testing
	session := &Session{
		runState: runState{
			taskQueue: make([]QueueItem, 0),
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &stream.NopInput{},
				Output: &stream.NopOutput{},
			},
		},
	}

	// Save session
	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		t.Fatal("Session file was not created")
	}

	// Load session
	loadedData, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify data was loaded
	if loadedData == nil {
		t.Error("Loaded data should not be nil")
	}
}

func TestLoadOrNewSession(t *testing.T) {
	// Use nil for provider since we're just testing session creation
	baseTools := []llm.Tool{}
	systemPrompt := "test system prompt"

	// Test creating a new session without specifying session file
	session, sessionFile, err := LoadOrNewSession(SessionConfig{
		BaseTools:    baseTools,
		SystemPrompt: systemPrompt,
		Input:        &stream.NopInput{},
		Output:       &stream.NopOutput{},
	})
	if err != nil {
		t.Fatalf("LoadOrNewSession returned error: %v", err)
	}
	if session == nil {
		t.Fatal("LoadOrNewSession returned nil session")
		return
	}

	if sessionFile != "" {
		t.Fatalf("LoadOrNewSession should return empty session file when not specified, got: %s", sessionFile)
	}

	// Verify SessionFile is empty in the session object
	if session.SessionFile != "" {
		t.Errorf("Session SessionFile should be empty when not specified, got: %s", session.SessionFile)
	}

	// Test manual save to a specific file
	testFile := "/tmp/test-session.md"
	if err := session.saveContentToFile(testFile, session.Contents); err != nil {
		t.Errorf("Failed to save session: %v", err)
	}
	defer os.Remove(testFile) // Clean up test file

	// Agent is lazily initialized, so it should be nil at startup
	if session.agent != nil {
		t.Error("Session agent should be nil at startup (lazy initialization)")
	}
}

// mockOutput is a simple mock for testing output
type mockOutput struct {
	writeCount int
	data       []byte
}

func (m *mockOutput) Write(p []byte) (n int, err error) {
	m.writeCount++
	m.data = append(m.data, p...)
	return len(p), nil
}

func TestSaveAndLoadSession_WithMessages(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-messages.md")

	// Create session with messages simulating a realistic agent conversation:
	// user → assistant(reasoning+text+toolcall) → tool(result) → assistant(reasoning+text)
	msgs := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{&llm.TextPart{Text: "Hello, world!"}},
		},
		{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				&llm.ReasoningPart{Text: "User needs help..."},
				&llm.TextPart{Text: "Let me help you."},
				&llm.ToolUsePart{
					ID:       "call_1",
					ToolName: "read_file",
					Input:    json.RawMessage(`{"path":"/tmp/test.txt"}`),
				},
			},
		},
		{
			Role: llm.RoleTool,
			Content: []llm.ContentPart{
				&llm.ToolResultPart{
					ID:      "call_1",
					Content: []llm.ContentPart{&llm.TextPart{Text: "file contents"}},
				},
			},
		},
		{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				&llm.ReasoningPart{Text: "Now I have the file..."},
				&llm.TextPart{Text: "Here is the file."},
			},
		},
	}
	session := &Session{
		runState: runState{
			Contents:  contentsFromMessagesForTest(msgs),
			taskQueue: make([]QueueItem, 0),
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &stream.NopInput{},
				Output: &stream.NopOutput{},
			},
		},
	}

	// Save
	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	// Load
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify messages - TLV format preserves message structure
	// 4 messages: user, assistant(reasoning+text+toolcall), tool(result), assistant(reasoning+text)
	if len(contentsToMessages(loaded.Contents)) != 4 {
		t.Fatalf("Message count mismatch: got %d, want 4", len(contentsToMessages(loaded.Contents)))
	}

	// Check first user message
	if contentsToMessages(loaded.Contents)[0].Role != llm.RoleUser {
		t.Errorf("First message role mismatch: got %s", contentsToMessages(loaded.Contents)[0].Role)
	}
	if len(contentsToMessages(loaded.Contents)[0].Content) != 1 {
		t.Fatalf("First message content parts: got %d", len(contentsToMessages(loaded.Contents)[0].Content))
	}
	if tp, ok := contentsToMessages(loaded.Contents)[0].Content[0].(*llm.TextPart); !ok || tp.Text != "Hello, world!" {
		t.Errorf("First message content mismatch: got %v", contentsToMessages(loaded.Contents)[0].Content[0])
	}

	// Check second message (assistant with reasoning + text + tool call)
	if contentsToMessages(loaded.Contents)[1].Role != llm.RoleAssistant {
		t.Errorf("Second message role mismatch: got %s", contentsToMessages(loaded.Contents)[1].Role)
	}
	if len(contentsToMessages(loaded.Contents)[1].Content) != 3 {
		t.Fatalf("Second message should have 3 parts (reasoning + text + toolcall), got %d", len(contentsToMessages(loaded.Contents)[1].Content))
	}
	if _, ok := contentsToMessages(loaded.Contents)[1].Content[0].(*llm.ReasoningPart); !ok {
		t.Errorf("Second message first part should be ReasoningPart, got %T", contentsToMessages(loaded.Contents)[1].Content[0])
	}
	if tp, ok := contentsToMessages(loaded.Contents)[1].Content[1].(*llm.TextPart); !ok || tp.Text != "Let me help you." {
		t.Errorf("Second message text part mismatch: got %v", contentsToMessages(loaded.Contents)[1].Content[1])
	}
	if _, ok := contentsToMessages(loaded.Contents)[1].Content[2].(*llm.ToolUsePart); !ok {
		t.Errorf("Second message third part should be ToolUsePart, got %T", contentsToMessages(loaded.Contents)[1].Content[2])
	}

	// Check third message (tool result)
	if contentsToMessages(loaded.Contents)[2].Role != llm.RoleTool {
		t.Errorf("Third message role mismatch: got %s", contentsToMessages(loaded.Contents)[2].Role)
	}

	// Check fourth message (assistant with reasoning + text)
	if contentsToMessages(loaded.Contents)[3].Role != llm.RoleAssistant {
		t.Errorf("Fourth message role mismatch: got %s", contentsToMessages(loaded.Contents)[3].Role)
	}
	if len(contentsToMessages(loaded.Contents)[3].Content) != 2 {
		t.Fatalf("Fourth message should have 2 parts (reasoning + text), got %d", len(contentsToMessages(loaded.Contents)[3].Content))
	}
	if _, ok := contentsToMessages(loaded.Contents)[3].Content[0].(*llm.ReasoningPart); !ok {
		t.Errorf("Fourth message first part should be ReasoningPart, got %T", contentsToMessages(loaded.Contents)[3].Content[0])
	}
	if tp, ok := contentsToMessages(loaded.Contents)[3].Content[1].(*llm.TextPart); !ok || tp.Text != "Here is the file." {
		t.Errorf("Fourth message text part mismatch: got %v", contentsToMessages(loaded.Contents)[3].Content[1])
	}
}

func TestMarkdownFormat_HumanReadable(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "readable.md")

	msgs := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{&llm.TextPart{Text: "Hello!\nHow are you?"}},
		},
		{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{&llm.TextPart{Text: "I'm doing well, thanks!"}},
		},
	}
	session := &Session{
		runState: runState{
			Contents:  contentsFromMessagesForTest(msgs),
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

	// Read raw file content
	raw, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(raw)

	// Verify YAML frontmatter is human-readable
	if !strings.Contains(content, "updated_at:") {
		t.Error("Missing updated_at in frontmatter")
	}

	// Verify message content is preserved (after NUL separators)
	if !strings.Contains(content, "Hello!") {
		t.Error("Missing user message content")
	}
	if !strings.Contains(content, "I'm doing well") {
		t.Error("Missing assistant message content")
	}
}

func TestReasoningOnlyMessage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "reasoning-only.md")

	// Session with assistant message that only has reasoning (no text)
	msgs := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{&llm.TextPart{Text: "What is lisp?"}},
		},
		{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{&llm.ReasoningPart{Text: "The user is asking about Lisp. I should explain it."}},
		},
	}
	session := &Session{
		runState: runState{
			Contents:  contentsFromMessagesForTest(msgs),
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

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	if len(contentsToMessages(loaded.Contents)) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(contentsToMessages(loaded.Contents)))
	}

	// Check first message
	if contentsToMessages(loaded.Contents)[0].Role != llm.RoleUser {
		t.Errorf("First message should be user, got %s", contentsToMessages(loaded.Contents)[0].Role)
	}

	// Check second message (reasoning only)
	if contentsToMessages(loaded.Contents)[1].Role != llm.RoleAssistant {
		t.Errorf("Second message should be assistant, got %s", contentsToMessages(loaded.Contents)[1].Role)
	}
	if len(contentsToMessages(loaded.Contents)[1].Content) != 1 {
		t.Fatalf("Second message should have 1 part, got %d", len(contentsToMessages(loaded.Contents)[1].Content))
	}
	if rp, ok := contentsToMessages(loaded.Contents)[1].Content[0].(*llm.ReasoningPart); !ok {
		t.Errorf("Second message part should be ReasoningPart")
	} else if !strings.Contains(rp.Text, "asking about Lisp") {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}
}

func TestTextAndReasoningInSameMessage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "text-and-reasoning.md")

	// Session with assistant message that has both reasoning and text.
	// After save/load, they must remain in the SAME assistant message so that
	// providers that require reasoning_content/thinking to be passed back receive it.
	msgs := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{&llm.TextPart{Text: "What is lisp?"}},
		},
		{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				&llm.ReasoningPart{Text: "Let me explain Lisp."},
				&llm.TextPart{Text: "Lisp is a family of programming languages."},
			},
		},
	}
	session := &Session{
		runState: runState{
			Contents:  contentsFromMessagesForTest(msgs),
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

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Reasoning and text must stay in the same assistant message
	if len(contentsToMessages(loaded.Contents)) != 2 {
		t.Fatalf("Expected 2 messages (user, assistant with reasoning+text), got %d", len(contentsToMessages(loaded.Contents)))
	}

	// Check first message is user
	if contentsToMessages(loaded.Contents)[0].Role != llm.RoleUser {
		t.Errorf("First message should be user, got %s", contentsToMessages(loaded.Contents)[0].Role)
	}

	// Check second message is assistant with reasoning + text in one message
	if contentsToMessages(loaded.Contents)[1].Role != llm.RoleAssistant {
		t.Errorf("Second message should be assistant, got %s", contentsToMessages(loaded.Contents)[1].Role)
	}
	if len(contentsToMessages(loaded.Contents)[1].Content) != 2 {
		t.Fatalf("Second message should have 2 parts (reasoning + text), got %d", len(contentsToMessages(loaded.Contents)[1].Content))
	}
	if rp, ok := contentsToMessages(loaded.Contents)[1].Content[0].(*llm.ReasoningPart); !ok {
		t.Errorf("Second message first part should be ReasoningPart, got %T", contentsToMessages(loaded.Contents)[1].Content[0])
	} else if rp.Text != "Let me explain Lisp." {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}
	if tp, ok := contentsToMessages(loaded.Contents)[1].Content[1].(*llm.TextPart); !ok {
		t.Errorf("Second message second part should be TextPart, got %T", contentsToMessages(loaded.Contents)[1].Content[1])
	} else if tp.Text != "Lisp is a family of programming languages." {
		t.Errorf("Text mismatch: %s", tp.Text)
	}
}

func TestModelSetWhileTaskRunning(t *testing.T) {
	// Create a mock output to capture messages
	output := &MockOutput{}

	// Create a session with a model manager
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	defer sessionCancel()
	session := &Session{
		runState: runState{
			taskQueue: make([]QueueItem, 0),
		},
		sessionConfig: sessionConfig{
			ModelManager: NewModelManager(""),
			SessionConfig: SessionConfig{
				Input:  &stream.NopInput{},
				Output: output,
			},
		},
		sharedState: sharedState{
			sessionCtx: sessionCtx,
		},
	}

	// Add a test model to the manager
	testModel := ModelConfig{
		ID:           1,
		Name:         "Test Model",
		ProtocolType: "openai",
		BaseURL:      "https://api.test.com/v1",
		APIKey:       "test-key",
		ModelName:    "test-model",
	}
	session.ModelManager.models = append(session.ModelManager.models, testModel)

	// Test 1: model_set should work when no task is running.
	// Dispatch through handleInputMsg (the real entry point) so the
	// ScheduleIdle policy is enforced.
	output.Messages = nil
	session.handleInputMsg(inputMsg{text: "model_set 1", isCmd: true})

	foundError := false
	for _, msg := range output.Messages {
		if strings.Contains(msg, `"type":"error"`) {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed when no task is running, but got error")
	}

	// Test 2: model_set should fail when task is running.
	output.Messages = nil
	session.inProgress = true
	session.handleInputMsg(inputMsg{text: "model_set 1", isCmd: true})

	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, `"type":"error"`) {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Error("model_set should fail when task is running, but no error was found")
	}

	// Test 3: model_set should work again after task completes.
	output.Messages = nil
	session.inProgress = false
	session.handleInputMsg(inputMsg{text: "model_set 1", isCmd: true})

	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, `"type":"error"`) {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed after task completes, but got error")
	}
}

func TestDisplayMessagesWithToolCalls(t *testing.T) {
	msgs := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{&llm.TextPart{Text: "List files"}},
		},
		{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{&llm.TextPart{Text: "I'll list files for you."}},
		},
		{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				&llm.ToolUsePart{
					ID:       "call_123",
					ToolName: "execute_command",
					Input:    json.RawMessage(`{"command": "ls -la"}`),
				},
			},
		},
		{
			Role: llm.RoleTool,
			Content: []llm.ContentPart{
				&llm.ToolResultPart{
					ID:      "call_123",
					Content: []llm.ContentPart{&llm.TextPart{Text: "file1.txt\nfile2.txt"}},
				},
			},
		},
		{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{&llm.TextPart{Text: "Found 2 files!"}},
		},
	}

	// Create session data with Content (source of truth)
	sessionData := &SessionData{
		Contents: contentsFromMessagesForTest(msgs),
		SessionMeta: SessionMeta{
			MessageVersion: MessageVersion,
			UpdatedAt:      time.Now(),
		},
	}

	// Generate RawTLV using the actual formatting function
	raw, err := formatSessionMarkdown(sessionData)
	if err != nil {
		t.Fatalf("Failed to format session: %v", err)
	}

	// Parse back
	loadedData, err := LoadSessionFromBytes(raw)
	if err != nil {
		t.Fatalf("Failed to load session: %v", err)
	}

	// Verify content was populated
	if len(loadedData.Contents) == 0 {
		t.Error("Content should be populated after loading")
	}

	// Create mock output and send content parts
	mockOutput := &mockOutput{}
	for _, part := range loadedData.Contents {
		tag, content, err := contentPartToTLV(part)
		if err != nil {
			t.Fatalf("Failed to serialize part: %v", err)
		}
		_ = stream.WriteTLV(mockOutput, tag, stream.WrapDelta(strconv.FormatUint(part.GetHistoryID(), 10), content)) //nolint:errcheck
	}

	// Verify that output was written
	if mockOutput.writeCount == 0 {
		t.Error("Content did not write any output")
	}

	// Parse the output data to check what TLV was sent
	outputStr := string(mockOutput.data)

	// User message should be in TLV
	if !strings.Contains(outputStr, "List files") {
		t.Error("User message should be in TLV")
	}

	// Assistant messages should be in TLV
	if !strings.Contains(outputStr, "I'll list files for you") {
		t.Error("First assistant message should be in TLV")
	}
	if !strings.Contains(outputStr, "Found 2 files!") {
		t.Error("Second assistant message should be in TLV")
	}

	// Tool call should be in TLV as FC tag
	if !strings.Contains(outputStr, "execute_command") {
		t.Error("Tool call should be in TLV")
	}
}

// LoadSessionFromBytes loads a session from raw bytes (for testing)
func LoadSessionFromBytes(data []byte) (*SessionData, error) {
	sd, err := parseSessionData(data)
	if err != nil {
		return nil, err
	}
	return sd, nil
}

func TestCleanIncompleteToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		messages []llm.Message
		wantLen  int // expected number of messages after cleaning
	}{
		{
			name: "complete tool call cycle",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{&llm.TextPart{Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					&llm.ToolUsePart{ID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				{Role: llm.RoleTool, Content: []llm.ContentPart{
					&llm.ToolResultPart{ID: "call-1", Content: []llm.ContentPart{&llm.TextPart{Text: "result"}}},
				}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{&llm.TextPart{Text: "Done"}}},
			},
			wantLen: 4, // all kept
		},
		{
			name: "complete tool call - Anthropic style (tool result in user message)",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{&llm.TextPart{Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					&llm.ToolUsePart{ID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// Anthropic puts tool result in user message
				{Role: llm.RoleUser, Content: []llm.ContentPart{
					&llm.ToolResultPart{ID: "call-1", Content: []llm.ContentPart{&llm.TextPart{Text: "result"}}},
				}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{&llm.TextPart{Text: "Done"}}},
			},
			wantLen: 4, // all kept
		},
		{
			name: "incomplete tool call - no result",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{&llm.TextPart{Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					&llm.ToolUsePart{ID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// No tool result message - this happens when API errors mid-cycle
			},
			wantLen: 1, // user kept, assistant removed (empty after filtering tool call)
		},
		{
			name: "incomplete tool call - assistant has text and tool call",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{&llm.TextPart{Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					&llm.TextPart{Text: "Let me help"},
					&llm.ToolUsePart{ID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// Tool call has no result
			},
			wantLen: 2, // user kept, assistant kept with only text part
		},
		{
			name: "incomplete tool call - Anthropic style (user message with tool result is missing)",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{&llm.TextPart{Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					&llm.ToolUsePart{ID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// No user message with tool result - incomplete
			},
			wantLen: 1, // only user message kept, assistant removed
		},
		{
			name: "trailing user message preserved",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{&llm.TextPart{Text: "First"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{&llm.TextPart{Text: "Response"}}},
				{Role: llm.RoleUser, Content: []llm.ContentPart{&llm.TextPart{Text: "Second (no response)"}}},
			},
			wantLen: 3, // all kept, including trailing user message
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// cleanIncompleteToolUses mutates in place — make a copy to avoid
			// cross-test contamination.
			msgs := make([]llm.Message, len(tt.messages))
			copy(msgs, tt.messages)
			got := cleanIncompleteToolUses(msgs)
			if len(got) != tt.wantLen {
				t.Errorf("cleanIncompleteToolUses() returned %d messages, want %d", len(got), tt.wantLen)
				for i, msg := range got {
					t.Logf("  msg[%d]: role=%s, parts=%d", i, msg.Role, len(msg.Content))
				}
			}
		})
	}
}

// TestTLVFormatRecursionProtection tests that the TLV format correctly handles
// session file content embedded in tool results (the recursion problem).
func TestTLVFormatRecursionProtection(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "recursion-test.md")

	// Create a session that contains what looks like session markers in tool output
	msgs := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: []llm.ContentPart{&llm.TextPart{Text: "Read the session file"}},
		},
		{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				&llm.ToolUsePart{
					ID:       "call1",
					ToolName: "read_file",
					Input:    json.RawMessage(`{"path": "old-session.md"}`),
				},
			},
		},
		{
			Role: llm.RoleTool,
			Content: []llm.ContentPart{
				&llm.ToolResultPart{
					ID:      "call1",
					Content: []llm.ContentPart{&llm.TextPart{Text: "---\nbase_url: https://api.test.com\n---\n\x00msg:user\nFake user message\n\x00msg:assistant\nFake assistant\n"}},
				},
			},
		},
		{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{&llm.TextPart{Text: "Here's the file content..."}},
		},
	}
	session := &Session{
		runState: runState{
			Contents:  contentsFromMessagesForTest(msgs),
			taskQueue: make([]QueueItem, 0),
		},
		sessionConfig: sessionConfig{
			SessionConfig: SessionConfig{
				Input:  &stream.NopInput{},
				Output: &stream.NopOutput{},
			},
		},
	}

	// Save
	if err := session.saveContentToFile(sessionPath, session.Contents); err != nil {
		t.Fatalf("saveContentToFile failed: %v", err)
	}

	// Load
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify we still have 4 messages (not more due to false parsing)
	if len(contentsToMessages(loaded.Contents)) != 4 {
		t.Errorf("expected 4 messages, got %d - recursion protection failed!", len(contentsToMessages(loaded.Contents)))
		for i, msg := range contentsToMessages(loaded.Contents) {
			t.Logf("msg[%d]: role=%s, parts=%d", i, msg.Role, len(msg.Content))
		}
		return
	}

	// Verify the tool result still contains the fake markers
	tr, ok := contentsToMessages(loaded.Contents)[2].Content[0].(*llm.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", contentsToMessages(loaded.Contents)[2].Content[0])
	}
	if len(tr.Content) == 0 {
		t.Fatalf("expected non-empty tool result content")
	}
	textPart, ok := tr.Content[0].(*llm.TextPart)
	if !ok {
		t.Fatalf("expected TextPart, got %T", tr.Content[0])
	}
	// The output should contain the fake markers (not stripped or misparsed)
	if !strings.Contains(textPart.Text, "msg:user") {
		t.Errorf("tool result should contain 'msg:user', got: %q", textPart.Text)
	}
	if !strings.Contains(textPart.Text, "Fake user message") {
		t.Errorf("tool result should contain 'Fake user message', got: %q", textPart.Text)
	}
}

// TestLoadSessionMissingReasoningLevel verifies that when a session file's
// frontmatter does not contain a reasoning_level key, the reasoning_level defaults
// to 1 (normal) rather than 0 (off).
func TestLoadSessionMissingReasoningLevel(t *testing.T) {
	// Frontmatter without reasoning_level, mimicking an older session file.
	// Must include message_version: 5 for the version check to pass.
	raw := []byte("---\nmessage_version: 5\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n")

	data, err := parseSessionData(raw)
	if err != nil {
		t.Fatalf("parseSessionData failed: %v", err)
	}

	if data.ReasoningLevel != 1 {
		t.Errorf("expected ReasoningLevel=1 when reasoning_level is absent from frontmatter, got %d", data.ReasoningLevel)
	}

	// Also verify that an explicit reasoning_level: 0 is preserved.
	raw2 := []byte("---\nmessage_version: 5\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\nreasoning_level: 0\n---\n")

	data2, err := parseSessionData(raw2)
	if err != nil {
		t.Fatalf("parseSessionData failed: %v", err)
	}

	if data2.ReasoningLevel != 0 {
		t.Errorf("expected ReasoningLevel=0 when reasoning_level is explicitly 0, got %d", data2.ReasoningLevel)
	}
}

// TestLoadSessionInvalidReasoningLevel verifies that out-of-range reasoning_level
// values in the session file are reset to the default (1) rather than being
// passed through to the provider.
func TestLoadSessionInvalidReasoningLevel(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{"negative", "reasoning_level: -1", 1},
		{"too high", "reasoning_level: 3", 1},
		{"large positive", "reasoning_level: 999", 1},
		{"large negative", "reasoning_level: -100", 1},
		{"valid zero", "reasoning_level: 0", 0},
		{"valid one", "reasoning_level: 1", 1},
		{"valid two", "reasoning_level: 2", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte("---\nmessage_version: 5\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n" + tt.value + "\n---\n")

			data, err := parseSessionData(raw)
			if err != nil {
				t.Fatalf("parseSessionData failed: %v", err)
			}

			if data.ReasoningLevel != tt.want {
				t.Errorf("reasoning_level=%s: expected %d, got %d", tt.value, tt.want, data.ReasoningLevel)
			}
		})
	}
}

// TestLoadSessionVersionMismatch verifies that a session file with a missing or
// non-matching version is rejected with ErrSessionVersionMismatch.
func TestLoadSessionVersionMismatch(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{
			name:    "missing version",
			raw:     "---\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n",
			wantErr: ErrSessionVersionMismatch,
		},
		{
			name:    "version zero",
			raw:     "---\nmessage_version: 0\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n",
			wantErr: ErrSessionVersionMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSessionData([]byte(tt.raw))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestLoadSessionVersionValid verifies that a session file with a valid version
// loads successfully.
func TestLoadSessionVersionValid(t *testing.T) {
	raw := []byte("---\nmessage_version: 5\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n")

	data, err := parseSessionData(raw)
	if err != nil {
		t.Fatalf("parseSessionData failed: %v", err)
	}

	if data.MessageVersion != 5 {
		t.Errorf("expected MessageVersion=5, got %d", data.MessageVersion)
	}
}

// TestLoadOrNewSessionVersionMismatch verifies that LoadOrNewSession returns an
// error when the session file has a non-matching version.
func TestLoadOrNewSessionVersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "old-session.md")

	// Write a session file with missing version
	if err := os.WriteFile(sessionPath, []byte("---\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n"), 0600); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}

	_, _, err := LoadOrNewSession(SessionConfig{
		SessionFile: sessionPath,
		Input:       &stream.NopInput{},
		Output:      &stream.NopOutput{},
	})
	if err == nil {
		t.Fatal("expected error for old session file, got nil")
	}
	if !errors.Is(err, ErrSessionVersionMismatch) {
		t.Errorf("expected ErrSessionVersionMismatch, got %v", err)
	}
}
