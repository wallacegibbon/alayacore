package agent

// Session compaction: compacts old messages to save context tokens.
//
// Three compaction strategies are applied to messages outside the recent window:
//
//  1. ReasoningPart — removed entirely. Raw thinking has no value once the step
//     is complete; the conclusion is captured in the TextPart.
//
//  2. ToolResultPart — removed along with its matching ToolCallPart.
//     The model can re-invoke the tool if it needs the data. Errors are preserved
//     since they're small and actionable.
//
//  3. ToolCallPart inputs — body content stripped from write_file and edit_file
//     calls (only for calls whose results are preserved, e.g. errors and skill
//     reads). The path is preserved so the model knows what was touched.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

// Compaction defaults — not user-configurable.
const (
	compactKeepSteps = 3 // number of recent agent steps to preserve in full

	toolNameWriteFile = "write_file"
	toolNameEditFile  = "edit_file"
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

// compactHistory compacts old messages to save context tokens.
// Messages from the most recent steps are preserved in full; older ones
// have reasoning stripped, tool call/result pairs removed, and empty
// messages cleaned up.
//
// Files under skill directories are never compacted — skill instructions
// and their supporting files must remain intact for the LLM to follow.
func (s *Session) compactHistory() {
	if s.NoCompact {
		return
	}
	// Each agent step is typically 2 messages (tool call + tool result)
	recentMessages := compactKeepSteps * 2
	msgs := s.Messages
	boundary := len(msgs) - recentMessages
	if boundary <= 0 {
		return
	}

	// Collect tool call IDs to keep from ALL messages: errors and skill reads.
	// Must scan all messages (not just old ones) because a tool result just past
	// the boundary still pairs with an old assistant tool call.
	keepIDs := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == llm.RoleAssistant {
			s.collectSkillDirReads(msg, keepIDs)
		}
		if msg.Role == llm.RoleTool {
			collectErrorResultIDs(msg, keepIDs)
		}
	}

	// Remove compactable tool call/result pairs and reasoning from old messages.
	dirty := false
	for i := 0; i < boundary; i++ {
		msg := &s.Messages[i]
		switch msg.Role {
		case llm.RoleAssistant:
			if compactAssistantParts(msg, keepIDs) {
				dirty = true
			}
		case llm.RoleTool:
			if compactToolResultParts(msg, keepIDs) {
				dirty = true
			}
		}
	}

	// Remove empty tool messages left after compaction.
	if dirty {
		s.Messages = removeEmptyToolMessages(s.Messages)
		s.sessionDirty = true
	}
}

// compactAssistantParts removes reasoning and tool calls whose results are
// also being removed. Tool calls in keepIDs are preserved (with their input
// compacted if applicable). Returns true if any content was modified.
func compactAssistantParts(msg *llm.Message, keepIDs map[string]bool) bool {
	changed := false
	filtered := msg.Content[:0] // reuse backing array
	for _, part := range msg.Content {
		switch p := part.(type) {
		case llm.ReasoningPart:
			changed = true // drop entirely — conclusion is in TextPart
		case llm.ToolCallPart:
			if keepIDs[p.ToolCallID] {
				// Preserved call (error or skill read) — compact input if large.
				if compacted := compactToolCallInput(p); compacted != nil {
					filtered = append(filtered, *compacted)
					changed = true
				} else {
					filtered = append(filtered, p)
				}
			} else {
				changed = true // drop — result is being removed too
			}
		default:
			filtered = append(filtered, p)
		}
	}
	if changed {
		msg.Content = filtered
	}
	return changed
}

// compactToolCallInput trims body content from write_file and edit_file inputs,
// preserving only the file path. Returns nil if no compaction was needed.
func compactToolCallInput(tc llm.ToolCallPart) *llm.ToolCallPart {
	if tc.ToolName != toolNameWriteFile && tc.ToolName != toolNameEditFile {
		return nil
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(tc.Input, &input); err != nil || input.Path == "" {
		return nil
	}
	// Already compacted (no content fields) — nothing to do.
	var check struct {
		Content   string `json:"content"`
		OldString string `json:"old_string"`
	}
	if err := json.Unmarshal(tc.Input, &check); err != nil {
		return nil
	}
	if check.Content == "" && check.OldString == "" {
		return nil
	}
	newInput, err := json.Marshal(map[string]string{"path": input.Path})
	if err != nil {
		return nil
	}
	result := tc
	result.Input = newInput
	return &result
}

// compactToolResultParts removes tool results whose calls are also being
// removed. Results for tool calls in keepIDs are preserved. Returns true if
// any content was modified.
func compactToolResultParts(msg *llm.Message, keepIDs map[string]bool) bool {
	changed := false
	filtered := msg.Content[:0]
	for _, part := range msg.Content {
		tr, ok := part.(llm.ToolResultPart)
		if !ok {
			filtered = append(filtered, part)
			continue
		}
		if keepIDs[tr.ToolCallID] {
			filtered = append(filtered, part)
			continue
		}
		changed = true // drop
	}
	if changed {
		msg.Content = filtered
	}
	return changed
}

// collectErrorResultIDs adds tool call IDs for error results to keepIDs.
func collectErrorResultIDs(msg llm.Message, keepIDs map[string]bool) {
	for _, part := range msg.Content {
		tr, ok := part.(llm.ToolResultPart)
		if !ok {
			continue
		}
		if _, isErr := tr.Output.(llm.ToolResultOutputError); isErr {
			keepIDs[tr.ToolCallID] = true
		}
	}
}

// removeEmptyToolMessages removes tool messages that have no content parts
// left after compaction.
func removeEmptyToolMessages(msgs []llm.Message) []llm.Message {
	result := msgs[:0]
	for _, msg := range msgs {
		if msg.Role == llm.RoleTool && len(msg.Content) == 0 {
			continue
		}
		result = append(result, msg)
	}
	return result
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
