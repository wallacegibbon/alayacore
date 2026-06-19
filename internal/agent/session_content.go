package agent

// Content part serialization, tag mapping, and ContentPart helpers.

import (
	"encoding/json"
	"fmt"

	"github.com/alayacore/alayacore/internal/llm"
)

// contentItem is the JSON representation of a ContentPart for TLV framing.
type contentItem struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	DataURI string `json:"data_uri,omitempty"`
}

// serializeContentParts serializes []ContentPart to JSON for TLV framing.
func serializeContentParts(parts []llm.ContentPart) (json.RawMessage, error) {
	items := make([]contentItem, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case *llm.TextPart:
			items = append(items, contentItem{Type: "text", Text: v.Text})
		case *llm.ImagePart:
			items = append(items, contentItem{Type: "image", DataURI: v.DataURI})
		case *llm.VideoPart:
			items = append(items, contentItem{Type: "video", DataURI: v.DataURI})
		case *llm.AudioPart:
			items = append(items, contentItem{Type: "audio", DataURI: v.DataURI})
		case *llm.DocumentPart:
			items = append(items, contentItem{Type: "document", DataURI: v.DataURI})
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

// contentsToMessages groups consecutive ContentParts with the same role into
// []llm.Message for API calls. Used on session load — during runtime both
// s.Contents and s.Messages are set together from the task result.
func contentsToMessages(contents []llm.ContentPart) []llm.Message {
	if len(contents) == 0 {
		return nil
	}
	msgs := make([]llm.Message, 0)
	for _, part := range contents {
		role := part.GetRole()
		if len(msgs) == 0 || msgs[len(msgs)-1].Role != role {
			msgs = append(msgs, llm.Message{Role: role})
		}
		msgs[len(msgs)-1].Contents = append(msgs[len(msgs)-1].Contents, part)
	}
	return msgs
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
			parts = append(parts, &llm.ImagePart{DataURI: item.DataURI})
		case "video":
			parts = append(parts, &llm.VideoPart{DataURI: item.DataURI})
		case "audio":
			parts = append(parts, &llm.AudioPart{DataURI: item.DataURI})
		case "document":
			parts = append(parts, &llm.DocumentPart{DataURI: item.DataURI})
		default:
			return nil, fmt.Errorf("unknown content part type in tool result: %s", item.Type)
		}
	}
	return parts, nil
}
