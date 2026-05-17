package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func (m *MockOutput) WriteString(s string) (int, error) {
	m.Messages = append(m.Messages, s)
	return len(s), nil
}

func (m *MockOutput) Flush() error {
	return nil
}

func TestSaveAndLoadSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-session.md")

	// Create test session data
	sessionData := &SessionData{
		Messages: []llm.Message{},
	}

	// Create a minimal session for testing
	session := &Session{
		Messages: sessionData.Messages,
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: &stream.NopOutput{},
		},
		taskQueue: make([]QueueItem, 0),
	}

	// Save session
	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
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

	// Verify data
	if len(loadedData.Messages) != len(sessionData.Messages) {
		t.Errorf("Messages mismatch: got %d, want %d", len(loadedData.Messages), len(sessionData.Messages))
	}
}

func TestLoadOrNewSession(t *testing.T) {
	// Use nil for provider since we're just testing session creation
	baseTools := []llm.Tool{}
	systemPrompt := "test system prompt"

	// Test creating a new session without specifying session file
	session, sessionFile := LoadOrNewSession(SessionConfig{
		BaseTools:    baseTools,
		SystemPrompt: systemPrompt,
		Input:        &stream.NopInput{},
		Output:       &stream.NopOutput{},
	})
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
	if err := session.saveSessionToFile(testFile); err != nil {
		t.Errorf("Failed to save session: %v", err)
	}
	defer os.Remove(testFile) // Clean up test file

	// Agent is lazily initialized, so it should be nil at startup
	if session.Agent != nil {
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

func (m *mockOutput) WriteString(s string) (int, error) {
	return m.Write([]byte(s))
}

func (m *mockOutput) Flush() error {
	return nil
}

func TestSaveAndLoadSession_WithMessages(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-messages.md")

	// Create session with messages simulating a realistic agent conversation:
	// user → assistant(reasoning+text+toolcall) → tool(result) → assistant(reasoning+text)
	session := &Session{
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hello, world!"}},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					llm.ReasoningPart{Type: "reasoning", Text: "User needs help..."},
					llm.TextPart{Type: "text", Text: "Let me help you."},
					llm.ToolCallPart{
						Type:       "tool_use",
						ToolCallID: "call_1",
						ToolName:   "read_file",
						Input:      json.RawMessage(`{"path":"/tmp/test.txt"}`),
					},
				},
			},
			{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{
					llm.ToolResultPart{
						Type:       "tool_result",
						ToolCallID: "call_1",
						Output:     llm.ToolResultOutputText{Type: "text", Text: "file contents"},
					},
				},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					llm.ReasoningPart{Type: "reasoning", Text: "Now I have the file..."},
					llm.TextPart{Type: "text", Text: "Here is the file."},
				},
			},
		},
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: &stream.NopOutput{},
		},
		taskQueue: make([]QueueItem, 0),
	}

	// Save
	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Load
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify messages - TLV format preserves message structure
	// 4 messages: user, assistant(reasoning+text+toolcall), tool(result), assistant(reasoning+text)
	if len(loaded.Messages) != 4 {
		t.Fatalf("Message count mismatch: got %d, want 4", len(loaded.Messages))
	}

	// Check first user message
	if loaded.Messages[0].Role != llm.RoleUser {
		t.Errorf("First message role mismatch: got %s", loaded.Messages[0].Role)
	}
	if len(loaded.Messages[0].Content) != 1 {
		t.Fatalf("First message content parts: got %d", len(loaded.Messages[0].Content))
	}
	if tp, ok := loaded.Messages[0].Content[0].(llm.TextPart); !ok || tp.Text != "Hello, world!" {
		t.Errorf("First message content mismatch: got %v", loaded.Messages[0].Content[0])
	}

	// Check second message (assistant with reasoning + text + tool call)
	if loaded.Messages[1].Role != llm.RoleAssistant {
		t.Errorf("Second message role mismatch: got %s", loaded.Messages[1].Role)
	}
	if len(loaded.Messages[1].Content) != 3 {
		t.Fatalf("Second message should have 3 parts (reasoning + text + toolcall), got %d", len(loaded.Messages[1].Content))
	}
	if _, ok := loaded.Messages[1].Content[0].(llm.ReasoningPart); !ok {
		t.Errorf("Second message first part should be ReasoningPart, got %T", loaded.Messages[1].Content[0])
	}
	if tp, ok := loaded.Messages[1].Content[1].(llm.TextPart); !ok || tp.Text != "Let me help you." {
		t.Errorf("Second message text part mismatch: got %v", loaded.Messages[1].Content[1])
	}
	if _, ok := loaded.Messages[1].Content[2].(llm.ToolCallPart); !ok {
		t.Errorf("Second message third part should be ToolCallPart, got %T", loaded.Messages[1].Content[2])
	}

	// Check third message (tool result)
	if loaded.Messages[2].Role != llm.RoleTool {
		t.Errorf("Third message role mismatch: got %s", loaded.Messages[2].Role)
	}

	// Check fourth message (assistant with reasoning + text)
	if loaded.Messages[3].Role != llm.RoleAssistant {
		t.Errorf("Fourth message role mismatch: got %s", loaded.Messages[3].Role)
	}
	if len(loaded.Messages[3].Content) != 2 {
		t.Fatalf("Fourth message should have 2 parts (reasoning + text), got %d", len(loaded.Messages[3].Content))
	}
	if _, ok := loaded.Messages[3].Content[0].(llm.ReasoningPart); !ok {
		t.Errorf("Fourth message first part should be ReasoningPart, got %T", loaded.Messages[3].Content[0])
	}
	if tp, ok := loaded.Messages[3].Content[1].(llm.TextPart); !ok || tp.Text != "Here is the file." {
		t.Errorf("Fourth message text part mismatch: got %v", loaded.Messages[3].Content[1])
	}
}

func TestMarkdownFormat_HumanReadable(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "readable.md")

	session := &Session{
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hello!\nHow are you?"}},
			},
			{
				Role:    llm.RoleAssistant,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "I'm doing well, thanks!"}},
			},
		},
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: &stream.NopOutput{},
		},
		taskQueue: make([]QueueItem, 0),
	}

	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
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
	session := &Session{
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "What is lisp?"}},
			},
			{
				Role:    llm.RoleAssistant,
				Content: []llm.ContentPart{llm.ReasoningPart{Type: "reasoning", Text: "The user is asking about Lisp. I should explain it."}},
			},
		},
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: &stream.NopOutput{},
		},
		taskQueue: make([]QueueItem, 0),
	}

	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	if len(loaded.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(loaded.Messages))
	}

	// Check first message
	if loaded.Messages[0].Role != llm.RoleUser {
		t.Errorf("First message should be user, got %s", loaded.Messages[0].Role)
	}

	// Check second message (reasoning only)
	if loaded.Messages[1].Role != llm.RoleAssistant {
		t.Errorf("Second message should be assistant, got %s", loaded.Messages[1].Role)
	}
	if len(loaded.Messages[1].Content) != 1 {
		t.Fatalf("Second message should have 1 part, got %d", len(loaded.Messages[1].Content))
	}
	if rp, ok := loaded.Messages[1].Content[0].(llm.ReasoningPart); !ok {
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
	session := &Session{
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "What is lisp?"}},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					llm.ReasoningPart{Type: "reasoning", Text: "Let me explain Lisp."},
					llm.TextPart{Type: "text", Text: "Lisp is a family of programming languages."},
				},
			},
		},
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: &stream.NopOutput{},
		},
		taskQueue: make([]QueueItem, 0),
	}

	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Load and verify
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Reasoning and text must stay in the same assistant message
	if len(loaded.Messages) != 2 {
		t.Fatalf("Expected 2 messages (user, assistant with reasoning+text), got %d", len(loaded.Messages))
	}

	// Check first message is user
	if loaded.Messages[0].Role != llm.RoleUser {
		t.Errorf("First message should be user, got %s", loaded.Messages[0].Role)
	}

	// Check second message is assistant with reasoning + text in one message
	if loaded.Messages[1].Role != llm.RoleAssistant {
		t.Errorf("Second message should be assistant, got %s", loaded.Messages[1].Role)
	}
	if len(loaded.Messages[1].Content) != 2 {
		t.Fatalf("Second message should have 2 parts (reasoning + text), got %d", len(loaded.Messages[1].Content))
	}
	if rp, ok := loaded.Messages[1].Content[0].(llm.ReasoningPart); !ok {
		t.Errorf("Second message first part should be ReasoningPart, got %T", loaded.Messages[1].Content[0])
	} else if rp.Text != "Let me explain Lisp." {
		t.Errorf("Reasoning text mismatch: %s", rp.Text)
	}
	if tp, ok := loaded.Messages[1].Content[1].(llm.TextPart); !ok {
		t.Errorf("Second message second part should be TextPart, got %T", loaded.Messages[1].Content[1])
	} else if tp.Text != "Lisp is a family of programming languages." {
		t.Errorf("Text mismatch: %s", tp.Text)
	}
}

func TestModelSetWhileTaskRunning(t *testing.T) {
	// Create a mock output to capture messages
	output := &MockOutput{}

	// Create a session with a model manager
	session := &Session{
		Messages: []llm.Message{},
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: output,
		},
		taskQueue:    make([]QueueItem, 0),
		ModelManager: NewModelManager(""),
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

	// Test 1: model_set should work when no task is running
	session.handleModelSet([]string{"test-model-1"})

	// Check that the model was switched (no error should be in output)
	foundError := false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "error") || strings.Contains(msg, "Error") {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed when no task is running, but got error")
	}

	// Test 2: model_set should fail when task is running
	output.Messages = nil // Clear previous messages
	session.inProgress = true
	session.handleModelSet([]string{"test-model-1"})

	// Check that the model was NOT switched (error should be in output)
	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "Cannot switch model while a task is running") {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Error("model_set should fail when task is running, but no error was found")
	}

	// Test 3: model_set should work again after task completes
	output.Messages = nil // Clear previous messages
	session.inProgress = false
	session.handleModelSet([]string{"test-model-1"})

	// Check that the model was switched (no error should be in output)
	foundError = false
	for _, msg := range output.Messages {
		if strings.Contains(msg, "error") || strings.Contains(msg, "Error") {
			foundError = true
			break
		}
	}
	if foundError {
		t.Error("model_set should succeed after task completes, but got error")
	}
}

func TestDisplayMessagesWithToolCalls(t *testing.T) {
	// Create session data with messages
	sessionData := &SessionData{
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "List files"}},
			},
			{
				Role:    llm.RoleAssistant,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "I'll list files for you."}},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					llm.ToolCallPart{
						Type:       "tool_use",
						ToolCallID: "call_123",
						ToolName:   "execute_command",
						Input:      json.RawMessage(`{"command": "ls -la"}`),
					},
				},
			},
			{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{
					llm.ToolResultPart{
						Type:       "tool_result",
						ToolCallID: "call_123",
						Output:     llm.ToolResultOutputText{Type: "text", Text: "file1.txt\nfile2.txt"},
					},
				},
			},
			{
				Role:    llm.RoleAssistant,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Found 2 files!"}},
			},
		},
		SessionMeta: SessionMeta{
			UpdatedAt: time.Now(),
		},
	}

	// Generate RawTLV using the actual formatting function
	raw, err := formatSessionMarkdown(sessionData)
	if err != nil {
		t.Fatalf("Failed to format session: %v", err)
	}

	// Parse back to extract TLVChunks
	loadedData, err := LoadSessionFromBytes(raw)
	if err != nil {
		t.Fatalf("Failed to load session: %v", err)
	}

	// Verify TLVChunks were populated
	if len(loadedData.TLVChunks) == 0 {
		t.Error("TLVChunks should be populated after loading")
	}

	// Create mock output and send TLV chunks
	mockOutput := &mockOutput{}
	for _, chunk := range loadedData.TLVChunks {
		_ = stream.WriteTLV(mockOutput, chunk.Tag, chunk.Value) //nolint:errcheck // test only
	}

	// Verify that output was written
	if mockOutput.writeCount == 0 {
		t.Error("TLVChunks did not write any output")
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
	return parseSessionMarkdown(data)
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
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					llm.ToolCallPart{Type: "tool_use", ToolCallID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				{Role: llm.RoleTool, Content: []llm.ContentPart{
					llm.ToolResultPart{Type: "tool_result", ToolCallID: "call-1", Output: llm.ToolResultOutputText{Type: "text", Text: "result"}},
				}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Done"}}},
			},
			wantLen: 4, // all kept
		},
		{
			name: "complete tool call - Anthropic style (tool result in user message)",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					llm.ToolCallPart{Type: "tool_use", ToolCallID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// Anthropic puts tool result in user message
				{Role: llm.RoleUser, Content: []llm.ContentPart{
					llm.ToolResultPart{Type: "tool_result", ToolCallID: "call-1", Output: llm.ToolResultOutputText{Type: "text", Text: "result"}},
				}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Done"}}},
			},
			wantLen: 4, // all kept
		},
		{
			name: "incomplete tool call - no result",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					llm.ToolCallPart{Type: "tool_use", ToolCallID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// No tool result message - this happens when API errors mid-cycle
			},
			wantLen: 1, // user kept, assistant removed (empty after filtering tool call)
		},
		{
			name: "incomplete tool call - assistant has text and tool call",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					llm.TextPart{Type: "text", Text: "Let me help"},
					llm.ToolCallPart{Type: "tool_use", ToolCallID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// Tool call has no result
			},
			wantLen: 2, // user kept, assistant kept with only text part
		},
		{
			name: "incomplete tool call - Anthropic style (user message with tool result is missing)",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Hello"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{
					llm.ToolCallPart{Type: "tool_use", ToolCallID: "call-1", ToolName: "test_tool", Input: json.RawMessage("{}")},
				}},
				// No user message with tool result - incomplete
			},
			wantLen: 1, // only user message kept, assistant removed
		},
		{
			name: "trailing user message preserved",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "First"}}},
				{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Response"}}},
				{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Second (no response)"}}},
			},
			wantLen: 3, // all kept, including trailing user message
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanIncompleteToolCalls(tt.messages)
			if len(got) != tt.wantLen {
				t.Errorf("cleanIncompleteToolCalls() returned %d messages, want %d", len(got), tt.wantLen)
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
	session := &Session{
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Read the session file"}},
			},
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					llm.ToolCallPart{
						Type:       "tool_use",
						ToolCallID: "call1",
						ToolName:   "read_file",
						Input:      json.RawMessage(`{"path": "old-session.md"}`),
					},
				},
			},
			{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{
					llm.ToolResultPart{
						Type:       "tool_result",
						ToolCallID: "call1",
						// This output contains text that looks like old session format markers!
						Output: llm.ToolResultOutputText{Type: "text", Text: "---\nbase_url: https://api.test.com\n---\n\x00msg:user\nFake user message\n\x00msg:assistant\nFake assistant\n"},
					},
				},
			},
			{
				Role:    llm.RoleAssistant,
				Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "Here's the file content..."}},
			},
		},
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: &stream.NopOutput{},
		},
		taskQueue: make([]QueueItem, 0),
	}

	// Save
	if err := session.saveSessionToFile(sessionPath); err != nil {
		t.Fatalf("saveSessionToFile failed: %v", err)
	}

	// Load
	loaded, err := LoadSession(sessionPath)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}

	// Verify we still have 4 messages (not more due to false parsing)
	if len(loaded.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d - recursion protection failed!", len(loaded.Messages))
		for i, msg := range loaded.Messages {
			t.Logf("msg[%d]: role=%s, parts=%d", i, msg.Role, len(msg.Content))
		}
		return
	}

	// Verify the tool result still contains the fake markers
	tr, ok := loaded.Messages[2].Content[0].(llm.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", loaded.Messages[2].Content[0])
	}
	output, ok := tr.Output.(llm.ToolResultOutputText)
	if !ok {
		t.Fatalf("expected ToolResultOutputText, got %T", tr.Output)
	}
	// The output should contain the fake markers (not stripped or misparsed)
	if !strings.Contains(output.Text, "msg:user") {
		t.Errorf("tool result should contain 'msg:user', got: %q", output.Text)
	}
	if !strings.Contains(output.Text, "Fake user message") {
		t.Errorf("tool result should contain 'Fake user message', got: %q", output.Text)
	}
}

// TestLoadSessionMissingThinkLevel verifies that when a session file's
// frontmatter does not contain a think_level key, the think_level defaults
// to 1 (normal) rather than 0 (off).
func TestLoadSessionMissingThinkLevel(t *testing.T) {
	// Frontmatter without think_level, mimicking an older session file.
	raw := []byte("---\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n---\n")

	data, err := parseSessionMarkdown(raw)
	if err != nil {
		t.Fatalf("parseSessionMarkdown failed: %v", err)
	}

	if data.ThinkLevel != 1 {
		t.Errorf("expected ThinkLevel=1 when think_level is absent from frontmatter, got %d", data.ThinkLevel)
	}

	// Also verify that an explicit think_level: 0 is preserved.
	raw2 := []byte("---\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\nthink_level: 0\n---\n")

	data2, err := parseSessionMarkdown(raw2)
	if err != nil {
		t.Fatalf("parseSessionMarkdown failed: %v", err)
	}

	if data2.ThinkLevel != 0 {
		t.Errorf("expected ThinkLevel=0 when think_level is explicitly 0, got %d", data2.ThinkLevel)
	}
}

// TestLoadSessionInvalidThinkLevel verifies that out-of-range think_level
// values in the session file are reset to the default (1) rather than being
// passed through to the provider.
func TestLoadSessionInvalidThinkLevel(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{"negative", "think_level: -1", 1},
		{"too high", "think_level: 3", 1},
		{"large positive", "think_level: 999", 1},
		{"large negative", "think_level: -100", 1},
		{"valid zero", "think_level: 0", 0},
		{"valid one", "think_level: 1", 1},
		{"valid two", "think_level: 2", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte("---\ncreated_at: 2024-01-15T10:30:00Z\nupdated_at: 2024-01-15T10:30:00Z\n" + tt.value + "\n---\n")

			data, err := parseSessionMarkdown(raw)
			if err != nil {
				t.Fatalf("parseSessionMarkdown failed: %v", err)
			}

			if data.ThinkLevel != tt.want {
				t.Errorf("think_level=%s: expected %d, got %d", tt.value, tt.want, data.ThinkLevel)
			}
		})
	}
}
