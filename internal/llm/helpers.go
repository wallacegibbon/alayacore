package llm

import (
	"context"
	"encoding/json"
)

// NewUserMessage creates a user message
func NewUserMessage(text string) Message {
	return Message{
		Role: RoleUser,
		Content: []ContentPart{
			TextPart{Text: text},
		},
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

// WithSchema sets the tool schema
func (b *ToolBuilder) WithSchema(schema json.RawMessage) *ToolBuilder {
	b.tool.Definition.Schema = schema
	return b
}

// WithExecute sets the execute function
func (b *ToolBuilder) WithExecute(fn func(ctx context.Context, input json.RawMessage) ([]ContentPart, error)) *ToolBuilder {
	b.tool.Execute = fn
	return b
}

// Build returns the tool
func (b *ToolBuilder) Build() Tool {
	return b.tool
}
