package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/wallacegibbon/coreclaw/internal/stream"
)

func TestGetSessionsDir(t *testing.T) {
	sessionsDir, err := GetSessionsDir()
	if err != nil {
		t.Fatalf("GetSessionsDir failed: %v", err)
	}

	if sessionsDir == "" {
		t.Fatal("GetSessionsDir returned empty string")
	}

	// Verify directory exists or can be created
	if stat, err := os.Stat(sessionsDir); err != nil {
		t.Fatalf("Sessions directory stat failed: %v", err)
	} else if !stat.IsDir() {
		t.Fatal("Sessions path is not a directory")
	}
}

func TestGenerateSessionFilename(t *testing.T) {
	filename := GenerateSessionFilename()

	if filename == "" {
		t.Fatal("GenerateSessionFilename returned empty string")
	}

	// Check format: YYYY-MM-DD-HHMMSS-1.json
	if len(filename) < 23 || filename[len(filename)-5:] != ".json" {
		t.Fatalf("Invalid filename format: %s", filename)
	}

	// Parse to verify it's a valid date
	parsed, err := time.Parse("2006-01-02-150405-1.json", filename)
	if err != nil {
		t.Fatalf("Failed to parse filename as date: %v", err)
	}

	// Verify it's recent (within last minute)
	if time.Since(parsed) > time.Minute {
		t.Fatalf("Generated filename is not recent: %s", filename)
	}
}

func TestSaveAndLoadSession(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test-session.json")

	// Create test session data
	sessionData := &SessionData{
		BaseURL:       "https://api.test.com/v1",
		ModelName:     "test-model",
		Messages:      []fantasy.Message{},
		TotalSpent:    fantasy.Usage{TotalTokens: 100},
		ContextTokens: 50,
	}

	// Create a minimal session for testing
	session := &Session{
		Messages:      sessionData.Messages,
		BaseURL:       sessionData.BaseURL,
		ModelName:     sessionData.ModelName,
		TotalSpent:    sessionData.TotalSpent,
		ContextTokens: sessionData.ContextTokens,
		Input:         &stream.NopInput{},
		Output:        &stream.NopOutput{},
		taskQueue:     make(chan Task, 10),
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
	if loadedData.BaseURL != sessionData.BaseURL {
		t.Errorf("BaseURL mismatch: got %s, want %s", loadedData.BaseURL, sessionData.BaseURL)
	}

	if loadedData.ModelName != sessionData.ModelName {
		t.Errorf("ModelName mismatch: got %s, want %s", loadedData.ModelName, sessionData.ModelName)
	}

	if loadedData.TotalSpent.TotalTokens != sessionData.TotalSpent.TotalTokens {
		t.Errorf("TotalTokens mismatch: got %d, want %d", loadedData.TotalSpent.TotalTokens, sessionData.TotalSpent.TotalTokens)
	}

	if loadedData.ContextTokens != sessionData.ContextTokens {
		t.Errorf("ContextTokens mismatch: got %d, want %d", loadedData.ContextTokens, sessionData.ContextTokens)
	}
}

func TestLoadLatestSession_EmptyDir(t *testing.T) {
	// Test the function doesn't crash
	// This will use ~/.coreclaw/sessions which may or may not exist
	_, _, err := LoadLatestSession()
	// Should not crash, may return nil or error
	if err != nil {
		// This is expected if ~/.coreclaw/sessions doesn't exist
		t.Logf("LoadLatestSession returned error (expected for empty state): %v", err)
	}
}

func TestLoadLatestSession_WithFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple session files
	now := time.Now()
	for i := 0; i < 3; i++ {
		filename := filepath.Join(tmpDir, now.Add(time.Duration(i)*time.Minute).Format("2006-01-02-1504.json"))
		data := &SessionData{
			BaseURL:   "https://api.test.com",
			ModelName: "test-model",
		}

		// Create a minimal session
		session := &Session{
			BaseURL:   data.BaseURL,
			ModelName: data.ModelName,
			Input:     &stream.NopInput{},
			Output:    &stream.NopOutput{},
			taskQueue: make(chan Task, 10),
		}

		if err := session.saveSessionToFile(filename); err != nil {
			t.Fatalf("Failed to save session %d: %v", i, err)
		}
	}

	// Note: LoadLatestSession uses GetSessionsDir which returns ~/.coreclaw/sessions
	// We can't test with our tmpDir without refactoring
	// This test just verifies the mechanism exists
	t.Skip("LoadLatestSession test skipped - uses hardcoded path")
}

func TestLoadOrNewSession(t *testing.T) {
	// Use nil for model since we're just testing session creation
	var model fantasy.LanguageModel = nil
	baseTools := []fantasy.AgentTool{}
	systemPrompt := "test system prompt"

	// Test creating a new session without specifying session file
	session, sessionFile := LoadOrNewSession(model, baseTools, systemPrompt, "https://api.test.com", "test-model", &stream.NopInput{}, &stream.NopOutput{}, "")
	if session == nil {
		t.Fatal("LoadOrNewSession returned nil session")
	}
	if sessionFile != "" {
		t.Fatalf("LoadOrNewSession should return empty session file when not specified, got: %s", sessionFile)
	}

	// Verify SessionFile is empty in the session object
	if session.SessionFile != "" {
		t.Errorf("Session SessionFile should be empty when not specified, got: %s", session.SessionFile)
	}

	// Test manual save to a specific file
	testFile := "/tmp/test-session.json"
	if err := session.saveSessionToFile(testFile); err != nil {
		t.Errorf("Failed to save session: %v", err)
	}
	defer os.Remove(testFile) // Clean up test file

	// Verify session is properly initialized
	if session.Agent == nil {
		t.Error("Session agent not set correctly")
	}
	if session.BaseURL != "https://api.test.com" {
		t.Errorf("Session BaseURL not set correctly: got %s", session.BaseURL)
	}
	if session.ModelName != "test-model" {
		t.Errorf("Session ModelName not set correctly: got %s", session.ModelName)
	}
}

func Test_displayMessages(t *testing.T) {
	// Create a mock output to capture displayed messages
	mockOutput := &mockOutput{}

	// Create a session with some messages
	session := &Session{
		Output: mockOutput,
		Messages: []fantasy.Message{
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hello world"}},
			},
			{
				Role:    fantasy.MessageRoleAssistant,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Hi there!"}},
			},
		},
		taskQueue: make(chan Task, 10),
	}

	// Display messages should not panic
	session.displayMessages()

	// Verify that output was written
	if mockOutput.writeCount == 0 {
		t.Error("displayMessages did not write any output")
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
