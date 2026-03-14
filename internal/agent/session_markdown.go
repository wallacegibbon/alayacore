package agent

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"charm.land/fantasy"
	"github.com/alayacore/alayacore/internal/stream"
	"gopkg.in/yaml.v3"
)

// ============================================================================
// Session File Format (TLV-encoded)
// ============================================================================

// Session file uses TLV (Tag-Length-Value) encoding to avoid recursion issues
// when session files contain tool results that might include session-like content.
// The format is: 2-byte tag + 4-byte length (big-endian) + content
// Tags are shared with stream package for consistency.

// Deprecated: old NUL-based separator (kept for backward compat during migration)
const msgSep = "\x00"

// formatSessionMarkdown converts SessionData to markdown format with TLV encoding.
// Format: YAML frontmatter + binary TLV-encoded messages
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf strings.Builder

	// Write YAML frontmatter
	meta := SessionMeta{
		BaseURL:       data.BaseURL,
		ModelName:     data.ModelName,
		TotalTokens:   data.TotalSpent.TotalTokens,
		ContextTokens: data.ContextTokens,
		CreatedAt:     data.CreatedAt,
		UpdatedAt:     data.UpdatedAt,
	}

	metaBytes, err := yaml.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}
	buf.WriteString("---\n")
	buf.Write(metaBytes)
	buf.WriteString("---\n")

	// Build binary section
	var binaryBuf strings.Builder
	for _, msg := range data.Messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			// Text content tags
			case fantasy.TextPart:
				tag := stream.TagTextUser
				if msg.Role == fantasy.MessageRoleAssistant {
					tag = stream.TagTextAssistant
				}
				writeTLV(&binaryBuf, tag, p.Text)

			case fantasy.ReasoningPart:
				writeTLV(&binaryBuf, stream.TagTextReasoning, p.Text)

			// Function tags
			case fantasy.ToolCallPart:
				// Encode tool call as JSON
				tc := toolCallData{
					ID:    p.ToolCallID,
					Name:  p.ToolName,
					Input: p.Input,
				}
				jsonData, err := json.Marshal(tc)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal tool call: %w", err)
				}
				writeTLV(&binaryBuf, stream.TagFunctionCall, string(jsonData))

			case fantasy.ToolResultPart:
				// Encode tool result as JSON
				tr := toolResultData{
					ID:     p.ToolCallID,
					Output: formatToolResultOutput(p.Output),
				}
				jsonData, err := json.Marshal(tr)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal tool result: %w", err)
				}
				writeTLV(&binaryBuf, stream.TagFunctionResult, string(jsonData))
			}
		}
	}

	buf.Write([]byte(binaryBuf.String()))
	return []byte(buf.String()), nil
}

// writeTLV writes a TLV-encoded entry with separator: \n\n + 2-byte tag + 4-byte length + content
func writeTLV(buf *strings.Builder, tag string, content string) {
	data := []byte(content)
	length := int32(len(data))

	buf.WriteString("\n\n") // Separator for readability
	buf.WriteByte(tag[0])
	buf.WriteByte(tag[1])
	binary.Write(buf, binary.BigEndian, length)
	buf.Write(data)
}

// toolCallData for JSON serialization
type toolCallData struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// toolResultData for JSON serialization
type toolResultData struct {
	ID     string `json:"id"`
	Output string `json:"output"`
}

// parseSessionMarkdown parses markdown format with TLV or legacy NUL separators.
func parseSessionMarkdown(data []byte) (*SessionData, error) {
	content := string(data)

	// Split frontmatter and body
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("session file missing YAML frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return nil, fmt.Errorf("session file missing frontmatter end marker")
	}

	frontmatter := content[4 : endIdx+4]
	body := content[endIdx+9:] // Skip "---\n" (4) + content + "\n---\n" (5)

	// Parse metadata
	var meta SessionMeta
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	sd := &SessionData{
		BaseURL:       meta.BaseURL,
		ModelName:     meta.ModelName,
		TotalSpent:    fantasy.Usage{TotalTokens: meta.TotalTokens},
		ContextTokens: meta.ContextTokens,
		CreatedAt:     meta.CreatedAt,
		UpdatedAt:     meta.UpdatedAt,
	}

	// Parse messages - try TLV first, fall back to legacy format
	if len(body) > 0 {
		msgs, err := parseMessagesTLV(body)
		if err != nil {
			// Fall back to legacy NUL-based format
			msgs, err = parseMessagesLegacy(body)
			if err != nil {
				return nil, err
			}
		}
		sd.Messages = msgs
	}

	return sd, nil
}

// parseMessagesTLV parses TLV-encoded message content.
func parseMessagesTLV(body string) ([]fantasy.Message, error) {
	var messages []fantasy.Message
	var currentMsg *fantasy.Message

	reader := strings.NewReader(body)

	for {
		// Skip newlines and whitespace before tag (for readability)
		for {
			b, err := reader.ReadByte()
			if err == io.EOF {
				// End of input
				if currentMsg != nil {
					messages = append(messages, *currentMsg)
				}
				return messages, nil
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read: %w", err)
			}
			if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
				// Found a non-whitespace byte - this is our tag
				reader.UnreadByte()
				break
			}
		}

		// Read tag (2 bytes)
		tagBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, tagBytes); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read tag: %w", err)
		}
		tag := string(tagBytes)

		// Read length (4 bytes big-endian)
		var length int32
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			return nil, fmt.Errorf("failed to read length: %w", err)
		}

		// Sanity check
		if length < 0 || length > 10*1024*1024 { // Max 10MB per message
			return nil, fmt.Errorf("invalid length: %d", length)
		}

		// Read content
		content := make([]byte, length)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("failed to read content: %w", err)
		}

		// Parse based on tag
		var msgPart fantasy.MessagePart
		var msgRole fantasy.MessageRole
		newMessage := false

		switch tag {
		// Text content tags
		case stream.TagTextUser:
			newMessage = true
			msgRole = fantasy.MessageRoleUser
			msgPart = fantasy.TextPart{Text: string(content)}

		case stream.TagTextAssistant:
			newMessage = true
			msgRole = fantasy.MessageRoleAssistant
			msgPart = fantasy.TextPart{Text: string(content)}

		case stream.TagTextReasoning:
			msgRole = fantasy.MessageRoleAssistant
			msgPart = fantasy.ReasoningPart{Text: string(content)}

		// Function tags
		case stream.TagFunctionCall:
			msgRole = fantasy.MessageRoleAssistant
			var tc toolCallData
			if err := json.Unmarshal(content, &tc); err != nil {
				return nil, fmt.Errorf("failed to parse tool call: %w", err)
			}
			msgPart = fantasy.ToolCallPart{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Input:      tc.Input,
			}

		case stream.TagFunctionResult:
			msgRole = fantasy.MessageRoleTool
			var tr toolResultData
			if err := json.Unmarshal(content, &tr); err != nil {
				return nil, fmt.Errorf("failed to parse tool result: %w", err)
			}
			msgPart = fantasy.ToolResultPart{
				ToolCallID: tr.ID,
				Output:     fantasy.ToolResultOutputContentText{Text: tr.Output},
			}

		default:
			return nil, fmt.Errorf("unknown tag: %s", tag)
		}

		// Create new message or append to current
		roleMismatch := currentMsg != nil && currentMsg.Role != msgRole
		if newMessage || currentMsg == nil || roleMismatch {
			if currentMsg != nil {
				messages = append(messages, *currentMsg)
			}
			currentMsg = &fantasy.Message{
				Role:    msgRole,
				Content: []fantasy.MessagePart{msgPart},
			}
		} else {
			currentMsg.Content = append(currentMsg.Content, msgPart)
		}
	}

	if currentMsg != nil {
		messages = append(messages, *currentMsg)
	}

	return messages, nil
}

// parseMessagesLegacy parses the old NUL-separated format for backward compatibility.
func parseMessagesLegacy(body string) ([]fantasy.Message, error) {
	var messages []fantasy.Message
	var currentMsg *fantasy.Message

	// Use a more robust parsing approach:
	// Only recognize NUL followed by known message types as separators.
	// This prevents embedded NUL characters in tool output from being
	// incorrectly parsed as message boundaries.
	parts := splitByMessageSeparators(body)

	for _, part := range parts {
		if part == "" {
			continue
		}

		// First line is role/type
		lines := strings.SplitN(part, "\n", 2)
		role := lines[0]
		content := ""
		if len(lines) > 1 {
			content = lines[1]
		}

		var msgPart fantasy.MessagePart
		var msgRole fantasy.MessageRole
		newMessage := false

		// Check for "msg:" prefix which indicates a new message
		if strings.HasPrefix(role, "msg:") {
			newMessage = true
			role = strings.TrimPrefix(role, "msg:")
		}

		switch role {
		case string(fantasy.MessageRoleUser):
			msgRole = fantasy.MessageRoleUser
			msgPart = fantasy.TextPart{Text: strings.TrimSuffix(content, "\n")}
		case string(fantasy.MessageRoleAssistant):
			msgRole = fantasy.MessageRoleAssistant
			msgPart = fantasy.TextPart{Text: strings.TrimSuffix(content, "\n")}
		case string(fantasy.MessageRoleTool):
			msgRole = fantasy.MessageRoleTool
			msgPart = parseToolResultContent(content)
		case "reasoning":
			msgRole = fantasy.MessageRoleAssistant
			msgPart = fantasy.ReasoningPart{Text: strings.TrimSuffix(content, "\n")}
		case "tool_call":
			msgRole = fantasy.MessageRoleAssistant
			msgPart = parseToolCallContent(content)
		case "tool_result":
			msgRole = fantasy.MessageRoleTool
			msgPart = parseToolResultContent(content)
		default:
			continue
		}

		// Create new message or append to current
		// Start a new message if: explicitly requested, no current message, or role mismatch
		roleMismatch := currentMsg != nil && currentMsg.Role != msgRole
		if newMessage || currentMsg == nil || roleMismatch {
			if currentMsg != nil {
				messages = append(messages, *currentMsg)
			}
			currentMsg = &fantasy.Message{
				Role:    msgRole,
				Content: []fantasy.MessagePart{msgPart},
			}
		} else {
			currentMsg.Content = append(currentMsg.Content, msgPart)
		}
	}

	if currentMsg != nil {
		messages = append(messages, *currentMsg)
	}

	return messages, nil
}

// parseToolCallContent parses tool_call section content.
func parseToolCallContent(content string) fantasy.ToolCallPart {
	tc := fantasy.ToolCallPart{}
	lines := strings.Split(content, "\n")

	var inputLines []string
	inInput := false

	for _, line := range lines {
		if inInput {
			inputLines = append(inputLines, strings.TrimPrefix(line, "  "))
		} else if strings.HasPrefix(line, "id: ") {
			tc.ToolCallID = strings.TrimPrefix(line, "id: ")
		} else if strings.HasPrefix(line, "name: ") {
			tc.ToolName = strings.TrimPrefix(line, "name: ")
		} else if line == "input:" {
			inInput = true
		}
	}

	tc.Input = strings.Join(inputLines, "\n")
	return tc
}

// parseToolResultContent parses tool_result section content.
func parseToolResultContent(content string) fantasy.ToolResultPart {
	tr := fantasy.ToolResultPart{}
	lines := strings.Split(content, "\n")

	var outputLines []string
	inOutput := false

	for _, line := range lines {
		if inOutput {
			outputLines = append(outputLines, strings.TrimPrefix(line, "  "))
		} else if strings.HasPrefix(line, "id: ") {
			tr.ToolCallID = strings.TrimPrefix(line, "id: ")
		} else if line == "output:" {
			inOutput = true
		}
	}

	output := strings.Join(outputLines, "\n")
	tr.Output = fantasy.ToolResultOutputContentText{Text: output}
	return tr
}

// formatToolResultOutput converts ToolResultOutputContent to string.
func formatToolResultOutput(output fantasy.ToolResultOutputContent) string {
	if text, ok := output.(fantasy.ToolResultOutputContentText); ok {
		return text.Text
	}
	if m, ok := output.(fantasy.ToolResultOutputContentMedia); ok {
		data, _ := json.Marshal(m)
		return string(data)
	}
	if e, ok := output.(fantasy.ToolResultOutputContentError); ok {
		return e.Error.Error()
	}
	return fmt.Sprintf("%v", output)
}

// indentString indents each line of a string.
func indentString(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

// splitByMessageSeparators splits the body by NUL character but only when
// followed by known message type markers. This prevents embedded NUL characters
// in tool output from being incorrectly parsed as message boundaries.
func splitByMessageSeparators(body string) []string {
	var parts []string
	var current strings.Builder

	// Known message type markers that can follow NUL
	markers := []string{
		"msg:user",
		"msg:assistant",
		"msg:tool",
		"msg:reasoning",
		"tool_call",
		"tool_result",
	}

	i := 0
	for i < len(body) {
		// Check if current position is NUL followed by a known marker
		if body[i] == 0x00 {
			found := false
			for _, marker := range markers {
				if i+1+len(marker) <= len(body) && body[i+1:i+1+len(marker)] == marker {
					// Found a valid separator - save current part
					if current.Len() > 0 {
						parts = append(parts, current.String())
						current.Reset()
					}
					i++ // Skip the NUL character
					found = true
					break
				}
			}
			if !found {
				// NUL not followed by a known marker - keep it in the content
				current.WriteByte(body[i])
				i++
			}
		} else {
			current.WriteByte(body[i])
			i++
		}
	}

	// Add the last part
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}
