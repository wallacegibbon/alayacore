package agent

// Session compaction: truncates old tool result outputs to save context tokens.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/truncation"
)

// cleanIncompleteToolCalls removes incomplete tool calls from messages.
func cleanIncompleteToolCalls(messages []llm.Message) []llm.Message {
	unmatchedCalls := make(map[string]bool)
	for _, msg := range messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case llm.ToolCallPart:
				unmatchedCalls[p.ToolCallID] = true
			case llm.ToolResultPart:
				delete(unmatchedCalls, p.ToolCallID)
			}
		}
	}

	if len(unmatchedCalls) == 0 {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		hasUnmatchedCall := false
		for _, part := range msg.Content {
			if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
				hasUnmatchedCall = true
				break
			}
		}

		if hasUnmatchedCall {
			filteredParts := make([]llm.ContentPart, 0, len(msg.Content))
			for _, part := range msg.Content {
				if tc, ok := part.(llm.ToolCallPart); ok && unmatchedCalls[tc.ToolCallID] {
					continue
				}
				filteredParts = append(filteredParts, part)
			}

			if len(filteredParts) > 0 {
				messages[i].Content = filteredParts
				return messages[:i+1]
			}
			messages = messages[:i]
			continue
		}

		return messages[:i+1]
	}

	return messages
}

// compactHistory truncates old tool result outputs to save context tokens.
// Only tool results from the most recent steps are kept in full; older ones
// are truncated to a summary. This prevents unbounded context growth in
// long agent sessions where each step's tool I/O accumulates.
//
// Files under skill directories are never truncated — skill instructions
// and their supporting files must remain intact for the LLM to follow.
func (s *Session) compactHistory() {
	if !s.compactEnabled {
		return
	}
	// Each agent step is typically 2 messages (tool call + tool result)
	recentMessages := s.compactKeepSteps * 2
	msgs := s.Messages
	truncateBoundary := len(msgs) - recentMessages
	if truncateBoundary <= 0 {
		return
	}

	skillReadIDs := make(map[string]bool)
	dirty := false
	for i := 0; i < truncateBoundary; i++ {
		msg := msgs[i]
		switch msg.Role {
		case llm.RoleAssistant:
			s.collectSkillDirReads(msg, skillReadIDs)
		case llm.RoleTool:
			if s.truncateToolResultsInMessage(msg, i, skillReadIDs, s.compactTruncateLen) {
				dirty = true
			}
		}
	}
	if dirty {
		s.sessionDirty = true
	}
}

// collectSkillDirReads extracts tool call IDs for read_file calls targeting
// files under any skill directory.
func (s *Session) collectSkillDirReads(msg llm.Message, skillReadIDs map[string]bool) {
	if len(s.skillDirs) == 0 {
		return
	}
	for _, part := range msg.Content {
		tc, ok := part.(llm.ToolCallPart)
		if !ok || tc.ToolName != "read_file" {
			continue
		}
		var input struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(tc.Input, &input); err != nil || input.Path == "" {
			continue
		}
		absPath := input.Path
		if abs, err := filepath.Abs(input.Path); err == nil {
			absPath = abs
		}
		for _, dir := range s.skillDirs {
			if strings.HasPrefix(absPath+string(os.PathSeparator), dir+string(os.PathSeparator)) {
				skillReadIDs[tc.ToolCallID] = true
				break
			}
		}
	}
}

// truncateToolResultsInMessage truncates tool results in a message,
// preserving reads from skill directories. Returns true if any content
// was actually truncated.
func (s *Session) truncateToolResultsInMessage(msg llm.Message, msgIndex int, skillReadIDs map[string]bool, maxLen int) bool {
	truncated := false
	for j, part := range msg.Content {
		tr, ok := part.(llm.ToolResultPart)
		if !ok {
			continue
		}
		if skillReadIDs[tr.ToolCallID] {
			continue // preserve skill directory reads
		}
		textOut, ok := tr.Output.(llm.ToolResultOutputText)
		if !ok {
			continue
		}
		result := truncation.Front(textOut.Text, maxLen, truncation.Marker)
		if len(result) == len(textOut.Text) {
			continue
		}
		s.Messages[msgIndex].Content[j] = llm.ToolResultPart{
			Type:       "tool_result",
			ToolCallID: tr.ToolCallID,
			Output:     llm.ToolResultOutputText{Type: "text", Text: result},
		}
		truncated = true
	}
	return truncated
}
