package llm

import (
	"context"
	"encoding/json"
	"strings"
)

// GroupByRole groups consecutive ContentParts with the same role into chunks.
// Returns a slice of chunks, each chunk having a uniform role.
// This is a shared helper used by both Anthropic and OpenAI providers
// to convert the flat ContentPart list into per-role API messages.
func GroupByRole(contents []ContentPart) [][]ContentPart {
	if len(contents) == 0 {
		return nil
	}
	var chunks [][]ContentPart
	i := 0
	for i < len(contents) {
		role := contents[i].GetRole()
		j := i
		for j < len(contents) && contents[j].GetRole() == role {
			j++
		}
		chunks = append(chunks, contents[i:j])
		i = j
	}
	return chunks
}

// ParseDataURI parses a data URI into media type and base64 data.
// Input: "data:image/jpeg;base64,/9j/4AAQ..."
// Output: "image/jpeg", "/9j/4AAQ...", true
// Returns ok=false for non-data URIs (e.g. plain URLs).
func ParseDataURI(uri string) (mediaType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	rest := uri[len(prefix):]
	semi := strings.IndexByte(rest, ';')
	if semi == -1 {
		return "", "", false
	}
	mediaType = rest[:semi]
	rest = rest[semi+1:]
	const b64Prefix = "base64,"
	if !strings.HasPrefix(rest, b64Prefix) {
		return "", "", false
	}
	return mediaType, rest[len(b64Prefix):], true
}

// NewUserContent creates user content parts
func NewUserContent(text string) []ContentPart {
	return []ContentPart{
		&TextPart{Text: text},
	}
}

// ToolBuilder helps build tool definitions
type ToolBuilder struct {
	tool Tool
}

// NewTool creates a new tool builder
func NewTool(name, description string) *ToolBuilder {
	return &ToolBuilder{
		tool: Tool{
			Definition: ToolDefinition{
				Name:        name,
				Description: description,
			},
		},
	}
}

func (b *ToolBuilder) WithSchema(schema json.RawMessage) *ToolBuilder {
	b.tool.Definition.Schema = schema
	return b
}

func (b *ToolBuilder) WithExecute(fn func(ctx context.Context, input json.RawMessage) ([]ContentPart, error)) *ToolBuilder {
	b.tool.Execute = fn
	return b
}

func (b *ToolBuilder) Build() Tool {
	return b.tool
}
