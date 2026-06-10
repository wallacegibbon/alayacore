package agent

// Content part serialization for TLV framing.
//
// serializeContentParts and deserializeContentParts convert between
// domain ContentPart slices (TextPart, ImagePart) and the JSON array
// format used in UF (tool result) TLV frames and session file storage.

import (
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/stream"
)

// serializeContentParts serializes []ContentPart to JSON for TLV framing.
// Supports TextPart and ImagePart. Other types are rejected as unsupported.
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

// tagForPart returns the TLV tag for a content part given its message role.
// This is the inverse of contentPartFromTLV's tag→role mapping.
func tagForPart(role llm.MessageRole, part llm.ContentPart) string {
	switch part.(type) {
	case llm.TextPart:
		if role == llm.RoleAssistant {
			return stream.TagAssistantT
		}
		return stream.TagUserT
	case llm.ImagePart:
		return stream.TagUserI
	case llm.ReasoningPart:
		return stream.TagAssistantR
	case llm.ToolUsePart:
		return stream.TagAssistantF
	case llm.ToolResultPart:
		return stream.TagUserF
	default:
		return stream.TagAssistantT
	}
}

// FindContentByID looks up a ContentItem by its history/stream ID.
// Searches from the end (most recent first) since adapter commands
// typically reference the latest content.
// Returns nil if not found.
func (s *Session) FindContentByID(id uint64) *ContentItem {
	for i := len(s.Content) - 1; i >= 0; i-- {
		if s.Content[i].ID == id {
			return &s.Content[i]
		}
	}
	return nil
}

// roleFromTag returns the llm.MessageRole corresponding to a TLV tag.
func roleFromTag(tag string) llm.MessageRole {
	switch tag {
	case stream.TagUserT, stream.TagUserI:
		return llm.RoleUser
	case stream.TagAssistantT, stream.TagAssistantR, stream.TagAssistantF:
		return llm.RoleAssistant
	case stream.TagUserF:
		return llm.RoleTool
	default:
		return llm.RoleUser
	}
}

// deserializeContentParts deserializes []ContentPart from JSON produced by serializeContentParts.
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
