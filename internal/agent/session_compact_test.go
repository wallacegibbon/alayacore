package agent

import (
	"encoding/json"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// helper to build a minimal Session for compaction tests.
func newTestSession(msgs []llm.Message) *Session {
	return &Session{
		Messages: msgs,
		SessionConfig: SessionConfig{
			Input:  &stream.NopInput{},
			Output: &stream.NopOutput{},
		},
		taskQueue: make([]QueueItem, 0),
	}
}

// messages that span 5 steps (10 messages), so the first 4 are compacted.
// Layout: user, assistant, tool, assistant, tool, assistant, tool, assistant, tool, assistant
func buildMultiStepMessages() []llm.Message {
	return []llm.Message{
		// Index 0 — user (old)
		{Role: llm.RoleUser, Content: []llm.ContentPart{
			llm.TextPart{Type: "text", Text: "Read config"},
		}},
		// Index 1 — assistant with reasoning + text + read_file call (old)
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "The user wants me to read the config file."},
			llm.TextPart{Type: "text", Text: "Let me read the config."},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "c1", ToolName: "read_file", Input: json.RawMessage(`{"path":"/etc/config.toml"}`)},
		}},
		// Index 2 — tool result for c1 (old)
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "c1", Output: llm.ToolResultOutputText{Type: "text", Text: "contents..."}},
		}},
		// Index 3 — assistant with reasoning + text + write_file + edit_file (old)
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "Now I need to write the updated version."},
			llm.TextPart{Type: "text", Text: "Now I'll update the config."},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "c2", ToolName: "write_file", Input: json.RawMessage(`{"path":"/etc/config.toml","content":"[database]\nhost = \"localhost\"\nport = 5432\n"}`)},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "c2b", ToolName: "edit_file", Input: json.RawMessage(`{"path":"/etc/defaults.toml","old_string":"timeout=30","new_string":"timeout=60"}`)},
		}},
		// Index 4 — tool results for c2, c2b (recent window starts here)
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "c2", Output: llm.ToolResultOutputText{Type: "text", Text: "File written successfully"}},
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "c2b", Output: llm.ToolResultOutputText{Type: "text", Text: "File edited successfully"}},
		}},
		// Index 5 — assistant with reasoning + text + search_content call (recent)
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "Let me search for other configs."},
			llm.TextPart{Type: "text", Text: "Config updated. Let me check for other configs."},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "c3", ToolName: "search_content", Input: json.RawMessage(`{"pattern":"database","path":"/etc"}`)},
		}},
		// Index 6 — tool result for c3 (recent)
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "c3", Output: llm.ToolResultOutputText{Type: "text", Text: "results..."}},
		}},
		// Index 7 — assistant with reasoning + text + edit_file call (recent)
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "Found other config files."},
			llm.TextPart{Type: "text", Text: "Found other configs. Updating now."},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "c4", ToolName: "edit_file", Input: json.RawMessage(`{"path":"/etc/other.conf","old_string":"database=sqlite","new_string":"database=postgres"}`)},
		}},
		// Index 8 — tool result for c4 (recent)
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "c4", Output: llm.ToolResultOutputText{Type: "text", Text: "File edited successfully"}},
		}},
		// Index 9 — assistant with reasoning + text (recent)
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "All configs updated."},
			llm.TextPart{Type: "text", Text: "All config files have been updated."},
		}},
	}
}

// After compaction of buildMultiStepMessages (boundary=4, indices 0-3 are old):
//   - Index 0: user — unchanged
//   - Index 1: assistant — reasoning and c1 removed, only text remains
//   - Index 2: tool — c1 result removed → empty → removed by removeEmptyToolMessages
//   - Index 3: assistant — reasoning, c2, c2b removed, only text remains
//
// Result: 9 messages (index 2 dropped).
func TestCompactHistory_ReasoningRemovedFromOld(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	s.compactHistory()

	for i, msg := range s.Messages {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		// Original indices 1, 3 are old → no reasoning.
		// After compaction they're at new indices 1, 2.
		if i >= 3 {
			break // past old window
		}
		for _, part := range msg.Content {
			if _, ok := part.(llm.ReasoningPart); ok {
				t.Errorf("msg[%d]: old reasoning should be removed", i)
			}
		}
	}
}

func TestCompactHistory_ReasoningPreservedInRecentWindow(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	s.compactHistory()

	// After compaction: 9 messages. Recent window = last 6 messages = indices 3-8.
	recentStart := len(s.Messages) - (compactKeepSteps * 2)
	for i := recentStart; i < len(s.Messages); i++ {
		msg := s.Messages[i]
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, part := range msg.Content {
			if _, ok := part.(llm.ReasoningPart); ok {
				return // found reasoning in recent — good
			}
		}
	}
	t.Error("recent assistant messages should still have reasoning")
}

func TestCompactHistory_ToolCallsRemovedFromOld(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	s.compactHistory()

	// Old assistant messages (indices 1, 2 after compaction) should have no ToolCallParts.
	for i := 0; i < 3; i++ {
		msg := s.Messages[i]
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, part := range msg.Content {
			if _, ok := part.(llm.ToolCallPart); ok {
				t.Errorf("msg[%d]: old tool call should be removed", i)
			}
		}
	}
}

func TestCompactHistory_EmptyToolMessagesRemoved(t *testing.T) {
	orig := buildMultiStepMessages()
	s := newTestSession(orig)
	s.compactHistory()

	// Original had 10 messages. After compaction, the tool message at index 2
	// (c1 result) is empty and should be removed → 9 messages.
	if len(s.Messages) >= len(orig) {
		t.Errorf("expected fewer messages after compaction, got %d (was %d)", len(s.Messages), len(orig))
	}

	// No empty tool messages should remain.
	for i, msg := range s.Messages {
		if msg.Role == llm.RoleTool && len(msg.Content) == 0 {
			t.Errorf("msg[%d]: empty tool message should be removed", i)
		}
	}
}

func TestCompactHistory_RecentToolResultsPreserved(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	s.compactHistory()

	recentStart := len(s.Messages) - (compactKeepSteps * 2)
	for i := recentStart; i < len(s.Messages); i++ {
		msg := s.Messages[i]
		if msg.Role != llm.RoleTool {
			continue
		}
		if len(msg.Content) == 0 {
			t.Errorf("msg[%d]: recent tool result was removed", i)
		}
	}
}

func TestCompactHistory_RecentEditFileInputPreserved(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	s.compactHistory()

	recentStart := len(s.Messages) - (compactKeepSteps * 2)
	for i := recentStart; i < len(s.Messages); i++ {
		msg := s.Messages[i]
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, part := range msg.Content {
			tc, ok := part.(llm.ToolCallPart)
			if !ok || tc.ToolName != "edit_file" {
				continue
			}
			var parsed struct {
				Path      string `json:"path"`
				OldString string `json:"old_string"`
			}
			if err := json.Unmarshal(tc.Input, &parsed); err != nil {
				t.Fatalf("failed to parse input: %v", err)
			}
			if parsed.OldString == "" {
				t.Error("recent edit_file input should not be compacted")
			}
		}
	}
}

func TestCompactHistory_ErrorResultsPreserved(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "do something"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "Let me try."},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "e1", ToolName: "execute_command", Input: json.RawMessage(`{"command":"bad-cmd"}`)},
		}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "e1", Output: llm.ToolResultOutputError{Type: "error", Error: "command not found: bad-cmd"}},
		}},
		// padding to reach boundary
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "ok"}}},
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "next"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "done"}}},
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "more"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "finished"}}},
	}
	s := newTestSession(msgs)
	s.compactHistory()

	// Error result should be preserved.
	tr := s.Messages[2].Content[0].(llm.ToolResultPart)
	if _, ok := tr.Output.(llm.ToolResultOutputError); !ok {
		t.Error("error results should be preserved")
	}

	// Error tool call should also be preserved in the assistant message.
	found := false
	for _, part := range s.Messages[1].Content {
		tc, ok := part.(llm.ToolCallPart)
		if ok && tc.ToolCallID == "e1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("error tool call should be preserved in assistant message")
	}
}

func TestCompactHistory_ErrorWriteFileInputCompacted(t *testing.T) {
	// A write_file call whose result is an error — the call is preserved but
	// its input is compacted (content stripped, path kept).
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "write it"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.TextPart{Type: "text", Text: "Writing file."},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "ew1", ToolName: "write_file", Input: json.RawMessage(`{"path":"/tmp/out.txt","content":"hello world"}`)},
		}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "ew1", Output: llm.ToolResultOutputError{Type: "error", Error: "permission denied"}},
		}},
		// padding
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "ok"}}},
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "next"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "done"}}},
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "more"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "finished"}}},
	}
	s := newTestSession(msgs)
	s.compactHistory()

	// Find the write_file call in the assistant message.
	msg := s.Messages[1]
	for _, part := range msg.Content {
		tc, ok := part.(llm.ToolCallPart)
		if !ok || tc.ToolName != "write_file" {
			continue
		}
		var parsed struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(tc.Input, &parsed); err != nil {
			t.Fatalf("failed to parse input: %v", err)
		}
		if parsed.Path != "/tmp/out.txt" {
			t.Errorf("path = %q, want /tmp/out.txt", parsed.Path)
		}
		if parsed.Content != "" {
			t.Errorf("content should be stripped, got %q", parsed.Content)
		}
		return
	}
	t.Error("write_file call should be preserved (its result is an error)")
}

func TestCompactHistory_SkillDirReadsPreserved(t *testing.T) {
	skillDir := "/tmp/test-skills"
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "load skill"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "Reading skill file."},
			llm.ToolCallPart{Type: "tool_use", ToolCallID: "s1", ToolName: "read_file", Input: json.RawMessage(`{"path":"` + skillDir + `/myskill.md"}`)},
		}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "s1", Output: llm.ToolResultOutputText{Type: "text", Text: "# My Skill\nDo important things."}},
		}},
		// padding
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "ok"}}},
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "next"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "done"}}},
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "more"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "finished"}}},
	}
	s := newTestSession(msgs)
	s.skillDirs = []string{skillDir}
	s.compactHistory()

	// Skill read result should be preserved.
	tr := s.Messages[2].Content[0].(llm.ToolResultPart)
	textOut, ok := tr.Output.(llm.ToolResultOutputText)
	if !ok {
		t.Fatal("expected ToolResultOutputText")
	}
	if textOut.Text != "# My Skill\nDo important things." {
		t.Errorf("skill read should be preserved, got %q", textOut.Text)
	}

	// Skill read tool call should also be preserved.
	found := false
	for _, part := range s.Messages[1].Content {
		tc, ok := part.(llm.ToolCallPart)
		if ok && tc.ToolCallID == "s1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("skill read tool call should be preserved")
	}

	// But reasoning should still be removed.
	for _, part := range s.Messages[1].Content {
		if _, ok := part.(llm.ReasoningPart); ok {
			t.Error("reasoning should still be removed even in skill steps")
		}
	}
}

func TestCompactHistory_Idempotent(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	s.compactHistory()
	first := len(s.Messages)

	s.sessionDirty = false
	s.compactHistory()

	if s.sessionDirty {
		t.Error("second compaction should not mark session dirty — already compacted")
	}
	if len(s.Messages) != first {
		t.Errorf("second compaction changed message count: %d → %d", first, len(s.Messages))
	}
}

func TestCompactHistory_NoCompactFlag(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	origLen := len(s.Messages[1].Content)
	s.NoCompact = true
	s.compactHistory()

	if len(s.Messages[1].Content) != origLen {
		t.Errorf("content modified despite NoCompact=true: got %d parts, want %d", len(s.Messages[1].Content), origLen)
	}
}

func TestCompactHistory_InsufficientMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "hi"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{
			llm.ReasoningPart{Type: "reasoning", Text: "thinking hard"},
			llm.TextPart{Type: "text", Text: "hello"},
		}},
	}
	s := newTestSession(msgs)
	s.compactHistory()

	rp := s.Messages[1].Content[0].(llm.ReasoningPart)
	if rp.Text != "thinking hard" {
		t.Errorf("reasoning should be untouched with few messages, got %q", rp.Text)
	}
}

func TestCompactHistory_DirtyFlagSet(t *testing.T) {
	s := newTestSession(buildMultiStepMessages())
	s.compactHistory()
	if !s.sessionDirty {
		t.Error("sessionDirty should be true after compaction modifies content")
	}
}

func TestCompactToolCallInput_WriteFile(t *testing.T) {
	tc := llm.ToolCallPart{
		Type:       "tool_use",
		ToolCallID: "w1",
		ToolName:   "write_file",
		Input:      json.RawMessage(`{"path":"/tmp/f","content":"hello"}`),
	}
	result := compactToolCallInput(tc)
	if result == nil {
		t.Fatal("write_file inputs should be compacted")
		return // satisfy staticcheck
	}
	var parsed struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(result.Input, &parsed); err != nil {
		t.Fatalf("compacted input should be valid JSON: %v", err)
	}
	if parsed.Path != "/tmp/f" {
		t.Errorf("path = %q, want /tmp/f", parsed.Path)
	}
	if parsed.Content != "" {
		t.Errorf("content should be stripped, got %q", parsed.Content)
	}
}

func TestCompactToolCallInput_EditFile(t *testing.T) {
	tc := llm.ToolCallPart{
		Type:       "tool_use",
		ToolCallID: "ed1",
		ToolName:   "edit_file",
		Input:      json.RawMessage(`{"path":"/tmp/f","old_string":"aaa","new_string":"bbb"}`),
	}
	result := compactToolCallInput(tc)
	if result == nil {
		t.Fatal("edit_file inputs should be compacted")
		return // satisfy staticcheck
	}
	var parsed struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
	}
	if err := json.Unmarshal(result.Input, &parsed); err != nil {
		t.Fatalf("compacted input should be valid JSON: %v", err)
	}
	if parsed.Path != "/tmp/f" {
		t.Errorf("path = %q, want /tmp/f", parsed.Path)
	}
	if parsed.OldString != "" {
		t.Errorf("old_string should be stripped, got %q", parsed.OldString)
	}
}

func TestCompactToolCallInput_OtherTools(t *testing.T) {
	tc := llm.ToolCallPart{
		Type:       "tool_use",
		ToolCallID: "sc1",
		ToolName:   "search_content",
		Input:      json.RawMessage(`{"pattern":"foo","path":"/tmp"}`),
	}
	result := compactToolCallInput(tc)
	if result != nil {
		t.Error("search_content inputs should not be compacted")
	}
}

func TestCompactToolCallInput_Idempotent(t *testing.T) {
	// Already compacted: only path field present.
	tc := llm.ToolCallPart{
		Type:       "tool_use",
		ToolCallID: "w1",
		ToolName:   "write_file",
		Input:      json.RawMessage(`{"path":"/tmp/f"}`),
	}
	result := compactToolCallInput(tc)
	if result != nil {
		t.Error("already-compacted input should return nil (no change needed)")
	}
}

func TestRemoveEmptyToolMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "hi"}}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{}}, // empty
		{Role: llm.RoleAssistant, Content: []llm.ContentPart{llm.TextPart{Type: "text", Text: "ok"}}},
		{Role: llm.RoleTool, Content: []llm.ContentPart{
			llm.ToolResultPart{Type: "tool_result", ToolCallID: "x1", Output: llm.ToolResultOutputText{Type: "text", Text: "result"}},
		}},
	}
	result := removeEmptyToolMessages(msgs)
	if len(result) != 3 {
		t.Errorf("expected 3 messages (empty tool removed), got %d", len(result))
	}
	if result[0].Role != llm.RoleUser || result[1].Role != llm.RoleAssistant || result[2].Role != llm.RoleTool {
		t.Error("unexpected message order after removal")
	}
}
