package agent

// Content part serialization, tag mapping, and ContentPart helpers.

import (
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
)

// contentItem is the JSON representation of a ContentPart for TLV framing.
type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URI  string `json:"uri,omitempty"`
}

// serializeContentParts serializes []ContentPart to JSON for TLV framing.
func serializeContentParts(parts []llm.ContentPart) (json.RawMessage, error) {
	items := make([]contentItem, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case *llm.TextPart:
			items = append(items, contentItem{Type: "text", Text: v.Text})
		case *llm.ImagePart:
			items = append(items, contentItem{Type: "image", URI: v.URI})
		case *llm.VideoPart:
			items = append(items, contentItem{Type: "video", URI: v.URI})
		case *llm.AudioPart:
			items = append(items, contentItem{Type: "audio", URI: v.URI})
		case *llm.DocumentPart:
			items = append(items, contentItem{Type: "document", URI: v.URI})
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

// deserializeContentParts deserializes []ContentPart from JSON produced by serializeContentParts.
func deserializeContentParts(data json.RawMessage) ([]llm.ContentPart, error) {
	if len(data) == 0 {
		return []llm.ContentPart{}, nil
	}
	var items []contentItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	parts := make([]llm.ContentPart, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "text":
			parts = append(parts, &llm.TextPart{Text: item.Text})
		case "image":
			parts = append(parts, &llm.ImagePart{URI: item.URI})
		case "video":
			parts = append(parts, &llm.VideoPart{URI: item.URI})
		case "audio":
			parts = append(parts, &llm.AudioPart{URI: item.URI})
		case "document":
			parts = append(parts, &llm.DocumentPart{URI: item.URI})
		default:
			return nil, fmt.Errorf("unknown content part type in tool result: %s", item.Type)
		}
	}
	return parts, nil
}
