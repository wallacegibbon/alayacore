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
	sd, err := parseSessionData(data)
	if err != nil {
		return nil, err
	}
	return sd, nil
}

func (s *Session) saveContentToFile(path string, content []llm.ContentPart) error {
	data := SessionData{
		SessionMeta: SessionMeta{
			MessageVersion: MessageVersion,
			CreatedAt:      s.CreatedAt,
			UpdatedAt:      time.Now(),
			ReasoningLevel: s.reasoningLevel,
			ActiveModel:    s.activeModelName(),
			ContextTokens:  s.ContextTokens,
		},
		Contents: content,
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

// formatSessionMarkdown serializes Content to TLV (without history IDs).
// History IDs are ephemeral and rebuilt on load.
func formatSessionMarkdown(data *SessionData) ([]byte, error) {
	var buf, tlvBuf strings.Builder
	buf.WriteString(formatFrontmatter(&data.SessionMeta))

	for _, part := range data.Contents {
		tag, content, err := contentPartToTLV(part)
		if err != nil {
			return nil, domainerrors.Wrap(CommandNameSave, err)
		}
		tlvBuf.WriteString("\n\n")
		tlvBuf.Write(stream.EncodeTLV(tag, content))
	}

	buf.WriteString(tlvBuf.String())
	return []byte(buf.String()), nil
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
		content, err := parseMessagesTLV(body)
		if err != nil {
			return nil, err
		}
		sd.Contents = content
	}

	return sd, nil
}

func parseMessagesTLV(body string) ([]llm.ContentPart, error) {
	est := len(body) / 64
	if est < 8 {
		est = 8
	}
	content := make([]llm.ContentPart, 0, est)

	reader := newTLVReader(body)
	var seqID uint64

	for {
		tag, raw, err := reader.read()
		if err == io.EOF {
			return content, nil
		}
		if err != nil {
			return content, fmt.Errorf("read error at chunk %d: %w", len(content), err)
		}

		msgPart, err := contentPartFromTLV(tag, raw)
		if err != nil {
			return content, fmt.Errorf("parse error at chunk %d (tag %q): %w", len(content), tag, err)
		}
		if msgPart == nil {
			continue
		}

		seqID++
		msgPart.SetHistoryID(seqID)
		content = append(content, msgPart)
	}
}

// contentPartToTLV serializes a ContentPart as a TLV tag and value string (without history ID).
func contentPartToTLV(part llm.ContentPart) (tag string, content string, err error) {
	switch p := part.(type) {
	case *llm.TextPart:
		if part.GetRole() == llm.RoleAssistant {
			return stream.TagAssistantT, p.Text, nil
		}
		return stream.TagUserT, p.Text, nil
	case *llm.ImagePart:
		return stream.TagUserI, p.DataURL, nil
	case *llm.VideoPart:
		return stream.TagUserV, p.DataURL, nil
	case *llm.AudioPart:
		return stream.TagUserA, p.DataURL, nil
	case *llm.DocumentPart:
		return stream.TagUserD, p.DataURL, nil
	case *llm.ReasoningPart:
		return stream.TagAssistantR, p.Text, nil
	case *llm.ToolUsePart:
		fd := stream.ToolUseData{ID: p.ID, Name: p.ToolName, Input: p.Input}
		jsonData, err := json.Marshal(fd)
		if err != nil {
			return "", "", err
		}
		return stream.TagAssistantF, string(jsonData), nil
	case *llm.ToolResultPart:
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

// contentPartFromTLV converts a TLV record into a ContentPart with Role set.
// History IDs in the TLV value are stripped — they are ephemeral and
// rebuilt when the session is loaded.
func contentPartFromTLV(tag string, content []byte) (llm.ContentPart, error) {
	cleanContent := string(content)
	if _, stripped, ok := stream.UnwrapDelta(cleanContent); ok {
		cleanContent = stripped
	}

	switch tag {
	case stream.TagUserT:
		return &llm.TextPart{Text: cleanContent, ContentMeta: llm.ContentMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserI:
		return &llm.ImagePart{DataURL: cleanContent, ContentMeta: llm.ContentMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserV:
		return &llm.VideoPart{DataURL: cleanContent, ContentMeta: llm.ContentMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserA:
		return &llm.AudioPart{DataURL: cleanContent, ContentMeta: llm.ContentMeta{Role: llm.RoleUser}}, nil
	case stream.TagUserD:
		return &llm.DocumentPart{DataURL: cleanContent, ContentMeta: llm.ContentMeta{Role: llm.RoleUser}}, nil
	case stream.TagAssistantT:
		return &llm.TextPart{Text: cleanContent, ContentMeta: llm.ContentMeta{Role: llm.RoleAssistant}}, nil
	case stream.TagAssistantR:
		return &llm.ReasoningPart{Text: cleanContent, ContentMeta: llm.ContentMeta{Role: llm.RoleAssistant}}, nil
	case stream.TagAssistantF:
		var fd stream.ToolUseData
		if err := json.Unmarshal([]byte(cleanContent), &fd); err != nil {
			return nil, fmt.Errorf("failed to parse function data: %w", err)
		}
		if fd.Name == "" {
			return nil, nil
		}
		return &llm.ToolUsePart{
			ID: fd.ID, ToolName: fd.Name, Input: fd.Input, ContentMeta: llm.ContentMeta{Role: llm.RoleAssistant},
		}, nil
	case stream.TagUserF:
		var tr stream.ToolResultData
		if err := json.Unmarshal([]byte(cleanContent), &tr); err != nil {
			return nil, fmt.Errorf("failed to parse tool result: %w", err)
		}
		contentParts, err := deserializeContentParts(tr.Output)
		if err != nil {
			return nil, fmt.Errorf("failed to parse tool result content: %w", err)
		}
		return &llm.ToolResultPart{ID: tr.ID, Content: contentParts, IsError: tr.IsError, ContentMeta: llm.ContentMeta{Role: llm.RoleTool}}, nil
	default:
		return nil, fmt.Errorf("unknown tag: %s", tag)
	}
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
