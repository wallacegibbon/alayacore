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
		return nil, domainerrors.Wrap(domainerrors.OpLoad, err)
	}
	return parseSessionMarkdown(data)
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
		return domainerrors.Wrap(domainerrors.OpSave, err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		return domainerrors.Wrap(domainerrors.OpSave, err)
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
		fd := stream.ToolUseData{ID: p.ID, Name: p.ToolName, Input: string(p.Input)}
		jsonData, err := json.Marshal(fd)
		if err != nil {
			return "", "", err
		}
		return stream.TagAssistantF, string(jsonData), nil
	case llm.ToolResultPart:
		tr := stream.ToolResultData{ID: p.ID, Output: formatToolResultOutput(p.Output)}
		_, tr.IsError = p.Output.(llm.ToolResultOutputFailed)
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
				return nil, domainerrors.Wrap(domainerrors.OpSave, err)
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

// parseSessionMarkdown parses markdown format with TLV encoding.
// parseFrontmatter extracts the frontmatter and body from content with "---" delimiters.
// Returns the frontmatter content (between the delimiters) and the body (after the closing delimiter).
func parseFrontmatter(content string) (frontmatter, body string, err error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", domainerrors.NewSessionErrorf(domainerrors.OpLoad, "session file missing frontmatter")
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return "", "", domainerrors.NewSessionErrorf(domainerrors.OpLoad, "session file missing frontmatter end marker")
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

func parseSessionMarkdown(data []byte) (*SessionData, error) {
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
			ID: fd.ID, ToolName: fd.Name, Input: json.RawMessage(fd.Input),
		}, nil
	case stream.TagUserF:
		var tr stream.ToolResultData
		if err := json.Unmarshal(content, &tr); err != nil {
			return "", nil, fmt.Errorf("failed to parse tool result: %w", err)
		}
		var output llm.ToolResultOutput
		if tr.IsError {
			output = llm.ToolResultOutputFailed{Reason: tr.Output}
		} else {
			output = llm.ToolResultOutputText{Text: tr.Output}
		}
		return llm.RoleTool, llm.ToolResultPart{ID: tr.ID, Output: output}, nil
	default:
		return "", nil, fmt.Errorf("unknown tag: %s", tag)
	}
}

func parseMessagesTLV(body string) ([]llm.Message, []TLVChunk, error) {
	var messages []llm.Message
	var chunks []TLVChunk
	var currentMsg *llm.Message

	reader := newTLVReader(body)
	for {
		tag, content, err := reader.read()
		if err == io.EOF {
			if currentMsg != nil {
				messages = append(messages, *currentMsg)
			}
			return messages, chunks, nil
		}
		if err != nil {
			return nil, nil, err
		}

		chunks = append(chunks, TLVChunk{Tag: tag, Value: string(content)})

		msgRole, msgPart, err := contentPartFromTLV(tag, content)
		if err != nil {
			return nil, nil, err
		}
		if msgPart == nil {
			continue // skip malformed
		}

		roleMismatch := currentMsg != nil && currentMsg.Role != msgRole
		if currentMsg == nil || roleMismatch {
			if currentMsg != nil {
				messages = append(messages, *currentMsg)
			}
			currentMsg = &llm.Message{Role: msgRole, Content: []llm.ContentPart{msgPart}}
		} else {
			currentMsg.Content = append(currentMsg.Content, msgPart)
		}
	}
}

func formatToolResultOutput(output llm.ToolResultOutput) string {
	if text, ok := output.(llm.ToolResultOutputText); ok {
		return text.Text
	}
	if e, ok := output.(llm.ToolResultOutputFailed); ok {
		return e.Reason
	}
	return fmt.Sprintf("%v", output)
}
