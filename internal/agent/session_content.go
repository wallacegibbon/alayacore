package agent

// Content part serialization, tag mapping, and ContentPart helpers.

import (
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
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
		case *llm.TextPart:
			items = append(items, contentItem{Type: "text", Text: v.Text})
		case *llm.ImagePart:
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

// FindContentByID looks up a ContentPart by its history/stream ID.
// Searches from the end (most recent first) since adapter commands
// typically reference the latest content.
// Returns nil if not found.
func (s *Session) FindContentByID(id uint64) *llm.ContentPart {
	for i := len(s.Content) - 1; i >= 0; i-- {
		if s.Content[i].GetHistoryID() == id {
			return &s.Content[i]
		}
	}
	return nil
}

// contentToMessages groups consecutive ContentParts with the same role into
// []llm.Message for API calls. Content is the source of truth — Messages is
// always derived from it.
func contentToMessages(content []llm.ContentPart) []llm.Message {
	if len(content) == 0 {
		return nil
	}
	msgs := make([]llm.Message, 0)
	for _, part := range content {
		role := part.GetRole()
		if len(msgs) == 0 || msgs[len(msgs)-1].Role != role {
			msgs = append(msgs, llm.Message{Role: role})
		}
		msgs[len(msgs)-1].Content = append(msgs[len(msgs)-1].Content, part)
	}
	return msgs
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
			parts = append(parts, &llm.TextPart{Text: item.Text})
		case "image":
			parts = append(parts, &llm.ImagePart{DataURL: item.DataURL})
		default:
			return nil, fmt.Errorf("unknown content part type in tool result: %s", item.Type)
		}
	}
	return parts, nil
}
