package agent

// Session persistence: saving, loading, and displaying sessions.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
)

// maxTLVContentSize is the safety limit for a single TLV record's content.
// No legitimate content part should come close to this; it catches corrupted
// session files attempting to allocate excessive memory.
const maxTLVContentSize = 10 * 1024 * 1024

// ErrSessionVersionMismatch is returned when a session file has a version
// that does not match MessageVersion and cannot be loaded.
var ErrSessionVersionMismatch = errors.New("session file version mismatch")

// ============================================================================
// Load/Save
// ============================================================================

// LoadSession loads a session from a file.
func LoadSession(path string) (*SessionData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, domainerrors.Wrap("load", err)
	}
	return parseSessionData(data)
}

func (s *Session) saveSessionToFile(path string) error {
	return s.saveSessionToFileWith(s.Messages, path)
}

func (s *Session) saveSessionToFileWith(messages []llm.Message, path string) error {
	data := SessionData{
		SessionMeta: SessionMeta{
			MessageVersion: MessageVersion,
			CreatedAt:      s.CreatedAt,
			UpdatedAt:      time.Now(),
			ReasoningLevel: int(s.reasoningLevel.Load()),
			ActiveModel:    s.activeModelName(),
			ContextTokens:  s.ContextTokens.Load(),
		},
		Messages: messages,
	}

	raw, err := formatSessionMarkdown(&data)
	if err != nil {
		return domainerrors.Wrap(CommandNameSave, err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return domainerrors.Wrap(CommandNameSave, err)
	}
	return nil
}

// ============================================================================
// Markdown Format (TLV encoding)
// ============================================================================

// formatFrontmatter writes the frontmatter using config.FormatKeyValue,
// which derives field names from `config` struct tags on SessionMeta.
func formatFrontmatter(meta *SessionMeta) string {
	return "---\n" + config.FormatKeyValue(meta) + "---\n"
}

// contentPartToTLV converts a ContentPart to a TLV tag and content string.
// Returns the TLV tag and the serialized content.
func contentPartToTLV(msgRole llm.MessageRole, part llm.ContentPart) (tag string, content string, err error) {
	switch p := part.(type) {
	case llm.TextPart:
		if msgRole == llm.RoleAssistant {
			return stream.TagAssistantT, p.Text, nil
		}
		return stream.TagUserT, p.Text, nil
	case llm.ImagePart:
		return stream.TagUserI, p.DataURL, nil
	case llm.ReasoningPart:
		return stream.TagAssistantR, p.Text, nil
	case llm.ToolUsePart:
		fd := stream.ToolUseData{ID: p.ID, Name: p.ToolName, Input: p.Input}
		jsonData, err := json.Marshal(fd)
		if err != nil {
			return "", "", err
		}
		return stream.TagAssistantF, string(jsonData), nil
	case llm.ToolResultPart:
		contentJSON, err := serializeContentParts(p.Content)
		if err != nil {
			return "", "", fmt.Errorf("failed to serialize tool result content: %w", err)
		}
		tr := stream.ToolResultData{ID: p.ID, Output: contentJSON, IsError: p.IsError}
		jsonData, err := json.Marshal(tr)
		if err != nil {
			return "", "", err
		}
		return stream.TagUserF, string(jsonData), nil
	default:
		return "", "", fmt.Errorf("unknown content part type: %T", part)
	}
}

// formatSessionMarkdown converts SessionData to markdown format with TLV encoding.
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf, tlvBuf strings.Builder
	buf.WriteString(formatFrontmatter(&data.SessionMeta))

	for _, msg := range data.Messages {
		for _, part := range msg.Content {
			tag, content, err := contentPartToTLV(msg.Role, part)
			if err != nil {
				return nil, domainerrors.Wrap(CommandNameSave, err)
			}
			writeTLV(&tlvBuf, tag, content)
		}
	}

	buf.WriteString(tlvBuf.String())
	return []byte(buf.String()), nil
}

func writeTLV(buf *strings.Builder, tag string, content string) {
	data := []byte(content)
	buf.WriteString("\n\n")
	buf.WriteByte(tag[0])
	buf.WriteByte(tag[1])
	buf.Write([]byte{
		byte(len(data) >> 24),
		byte(len(data) >> 16),
		byte(len(data) >> 8),
		byte(len(data)),
	})
	buf.Write(data)
}

// parseFrontmatter extracts the frontmatter and body from content with "---" delimiters.
// Returns the frontmatter content (between the delimiters) and the body (after the closing delimiter).
func parseFrontmatter(content string) (frontmatter, body string, err error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", domainerrors.NewSessionErrorf("load", "session file missing frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return "", "", domainerrors.NewSessionErrorf("load", "session file missing frontmatter end marker")
	}

	frontmatter = content[4 : endIdx+4]
	body = content[endIdx+9:]
	return frontmatter, body, nil
}

// parseSessionMeta parses key-value pairs from frontmatter into SessionMeta using struct tags.
// Returns an error if the session file version does not match MessageVersion.
func parseSessionMeta(frontmatter string) (SessionMeta, error) {
	var meta SessionMeta
	config.ParseKeyValue(frontmatter, &meta)

	// Check message format version — must match exactly.
	if meta.MessageVersion != MessageVersion {
		return meta, fmt.Errorf("%w: got %d, expected %d",
			ErrSessionVersionMismatch, meta.MessageVersion, MessageVersion)
	}

	// Default reasoning_level to 1 (normal) when the key is absent from the
	// frontmatter.  config.ParseKeyValue leaves the field at its zero value
	// (0) when the key is missing, which would incorrectly disable reasoning.
	if !strings.Contains(frontmatter, "reasoning_level:") {
		meta.ReasoningLevel = config.DefaultReasoningLevel
	}

	// Validate reasoning_level range: 0=off, 1=normal, 2=max.
	// Clamp to default if the stored value is out of range (e.g. corrupted
	// or hand-edited session file).
	if meta.ReasoningLevel < config.ReasoningLevelOff || meta.ReasoningLevel > config.ReasoningLevelMax {
		meta.ReasoningLevel = config.DefaultReasoningLevel
	}

	return meta, nil
}

// parseSessionData parses a session file (frontmatter + TLV body) into SessionData.
func parseSessionData(data []byte) (*SessionData, error) {
	frontmatter, body, err := parseFrontmatter(string(data))
	if err != nil {
		return nil, err
	}

	meta, err := parseSessionMeta(frontmatter)
	if err != nil {
		return nil, err
	}

	sd := &SessionData{
		SessionMeta: meta,
	}

	if len(body) > 0 {
		msgs, chunks, err := parseMessagesTLV(body)
		if err != nil {
			return nil, err
		}
		sd.Messages = msgs
		sd.TLVChunks = chunks
	}

	return sd, nil
}

// tlvReader reads sequential TLV records from a string.
type tlvReader struct {
	reader *strings.Reader
}

func newTLVReader(body string) *tlvReader {
	return &tlvReader{reader: strings.NewReader(body)}
}

// read advances to the next TLV record. Returns io.EOF when exhausted.
func (r *tlvReader) read() (tag string, content []byte, err error) {
	// Skip whitespace/newlines between records.
	for {
		b, err := r.reader.ReadByte()
		if err != nil {
			return "", nil, err
		}
		if b != '\n' && b != '\r' && b != ' ' && b != '\t' {
			r.reader.UnreadByte() //nolint:errcheck
			break
		}
	}

	tagBytes := make([]byte, 2)
	if _, err := io.ReadFull(r.reader, tagBytes); err != nil {
		return "", nil, err
	}

	var length int32
	if err := binary.Read(r.reader, binary.BigEndian, &length); err != nil {
		return "", nil, fmt.Errorf("failed to read length: %w", err)
	}
	if length < 0 || length > maxTLVContentSize {
		return "", nil, fmt.Errorf("invalid length: %d", length)
	}

	content = make([]byte, length)
	if _, err := io.ReadFull(r.reader, content); err != nil {
		return "", nil, fmt.Errorf("failed to read content: %w", err)
	}

	return string(tagBytes), content, nil
}

// contentPartFromTLV converts a TLV record into a ContentPart.
// Returns the message role and the content part.
func contentPartFromTLV(tag string, content []byte) (llm.MessageRole, llm.ContentPart, error) {
	switch tag {
	case stream.TagUserT:
		return llm.RoleUser, llm.TextPart{Text: string(content)}, nil
	case stream.TagUserI:
		return llm.RoleUser, llm.ImagePart{DataURL: string(content)}, nil
	case stream.TagAssistantT:
		return llm.RoleAssistant, llm.TextPart{Text: string(content)}, nil
	case stream.TagAssistantR:
		return llm.RoleAssistant, llm.ReasoningPart{Text: string(content)}, nil
	case stream.TagAssistantF:
		var fd stream.ToolUseData
		if err := json.Unmarshal(content, &fd); err != nil {
			return "", nil, fmt.Errorf("failed to parse function data: %w", err)
		}
		if fd.Name == "" {
			return "", nil, nil // skip malformed
		}
		return llm.RoleAssistant, llm.ToolUsePart{
			ID: fd.ID, ToolName: fd.Name, Input: fd.Input,
		}, nil
	case stream.TagUserF:
		var tr stream.ToolResultData
		if err := json.Unmarshal(content, &tr); err != nil {
			return "", nil, fmt.Errorf("failed to parse tool result: %w", err)
		}
		contentParts, err := deserializeContentParts(tr.Output)
		if err != nil {
			return "", nil, fmt.Errorf("failed to parse tool result content: %w", err)
		}
		return llm.RoleTool, llm.ToolResultPart{ID: tr.ID, Content: contentParts, IsError: tr.IsError}, nil
	default:
		return "", nil, fmt.Errorf("unknown tag: %s", tag)
	}
}

func parseMessagesTLV(body string) ([]llm.Message, []TLVChunk, error) {
	// Pre-allocate with a rough capacity estimate: each TLV record has at least
	// 6 bytes of overhead (2 tag + 4 length), so body/64 is a conservative
	// lower bound that avoids most reallocations for large sessions.
	est := len(body) / 64
	if est < 8 {
		est = 8
	}
	messages := make([]llm.Message, 0, est)
	chunks := make([]TLVChunk, 0, est)

	reader := newTLVReader(body)
	currentIdx := -1 // -1 means no message is being built

	for {
		tag, content, err := reader.read()
		if err == io.EOF {
			return messages, chunks, nil
		}
		if err != nil {
			return messages, chunks, fmt.Errorf("read error at chunk %d: %w", len(chunks), err)
		}

		chunks = append(chunks, TLVChunk{Tag: tag, Value: string(content)})

		msgRole, msgPart, err := contentPartFromTLV(tag, content)
		if err != nil {
			return messages, chunks, fmt.Errorf("parse error at chunk %d (tag %q): %w", len(chunks)-1, tag, err)
		}
		if msgPart == nil {
			// Malformed record (e.g. TagAssistantF with an empty tool name);
			// skip for messages but keep the raw chunk for display/debugging.
			continue
		}

		if currentIdx < 0 || messages[currentIdx].Role != msgRole {
			messages = append(messages, llm.Message{
				Role:    msgRole,
				Content: []llm.ContentPart{msgPart},
			})
			currentIdx = len(messages) - 1
		} else {
			messages[currentIdx].Content = append(messages[currentIdx].Content, msgPart)
		}
	}
}

// serializeContentParts serializes []ContentPart to JSON for persistence.
// Only TextPart and ImagePart are supported in tool results.
func serializeContentParts(parts []llm.ContentPart) (json.RawMessage, error) {
	type contentItem struct {
		Type    string `json:"type"`
		Text    string `json:"text,omitempty"`
		DataURL string `json:"data_url,omitempty"`
	}
	items := make([]contentItem, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case llm.TextPart:
			items = append(items, contentItem{Type: "text", Text: v.Text})
		case llm.ImagePart:
			items = append(items, contentItem{Type: "image", DataURL: v.DataURL})
		default:
			return nil, fmt.Errorf("unsupported content part type in tool result: %T", p)
		}
	}
	data, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// deserializeContentParts deserializes []ContentPart from JSON persisted by serializeContentParts.
func deserializeContentParts(data json.RawMessage) ([]llm.ContentPart, error) {
	if len(data) == 0 {
		return []llm.ContentPart{}, nil
	}
	var items []struct {
		Type    string `json:"type"`
		Text    string `json:"text,omitempty"`
		DataURL string `json:"data_url,omitempty"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	parts := make([]llm.ContentPart, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "text":
			parts = append(parts, llm.TextPart{Text: item.Text})
		case "image":
			parts = append(parts, llm.ImagePart{DataURL: item.DataURL})
		default:
			return nil, fmt.Errorf("unknown content part type in tool result: %s", item.Type)
		}
	}
	return parts, nil
}
