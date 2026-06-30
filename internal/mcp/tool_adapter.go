package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

// ToolNamingStrategy defines how MCP tool names are adapted to avoid
// conflicts with built-in tools and across MCP servers.
type ToolNamingStrategy int

const (
	// ToolNameKeep uses the original tool name.
	// Risk of collision if two servers expose tools with the same name.
	ToolNameKeep ToolNamingStrategy = iota

	// ToolNamePrefix prepends the server name followed by "_" to each tool.
	// E.g. "db_server_query" for server "db_server", tool "query".
	// This is the safest strategy.
	ToolNamePrefix
)

// defaultNaming is the naming strategy used by AlayaCore.
const defaultNaming = ToolNamePrefix

// ToolsToAgentTools converts a map of server→tools into a flat list
// of llm.Tool instances, using the configured naming strategy.
//
// The returned tools delegate execution to the original MCP server
// via the Manager.
func ToolsToAgentTools(serverTools map[string][]Tool, manager *Manager) []llm.Tool {
	return toolsToAgentTools(serverTools, manager, defaultNaming)
}

// ResourcesToAgentTools creates a read_resource tool for each server that
// advertised resource capability. The tool allows the LLM to read arbitrary
// resources by URI.
func ResourcesToAgentTools(clients []*Client, manager *Manager) []llm.Tool {
	result := make([]llm.Tool, 0, len(clients))
	for _, c := range clients {
		if c.State() != StateReady || !c.HasResources() {
			continue
		}
		serverName := c.Name()
		tool := newReadResourceTool(serverName, manager)
		result = append(result, tool)
	}
	return result
}

// PromptsToAgentTools creates a get_prompt tool for each server that
// advertised prompt capability.
func PromptsToAgentTools(clients []*Client, manager *Manager) []llm.Tool {
	result := make([]llm.Tool, 0, len(clients))
	for _, c := range clients {
		if c.State() != StateReady || !c.HasPrompts() {
			continue
		}
		serverName := c.Name()
		tool := newGetPromptTool(serverName, manager)
		result = append(result, tool)
	}
	return result
}

func toolsToAgentTools(serverTools map[string][]Tool, manager *Manager, strategy ToolNamingStrategy) []llm.Tool {
	var result []llm.Tool

	for serverName, tools := range serverTools {
		for _, tool := range tools {
			adapted, err := adaptTool(serverName, tool, manager, strategy)
			if err != nil {
				// Skip tools with invalid schemas. A single malformed tool
				// should not prevent other valid tools from being used.
				// The warning is intentionally discarded here — the caller
				// (app.go) already collects errors via MCPStartupErrors.
				continue
			}
			result = append(result, adapted)
		}
	}

	return result
}

// adaptTool converts a single MCP tool to an llm.Tool.
// Returns a zero-value Tool and an error if the tool has an invalid schema.
func adaptTool(serverName string, tool Tool, manager *Manager, strategy ToolNamingStrategy) (llm.Tool, error) {
	name := buildToolName(serverName, tool.Name, strategy)
	description := buildDescription(serverName, tool.Description, tool.Annotations)

	schema, err := sanitizeInputSchema(tool.InputSchema)
	if err != nil {
		return llm.Tool{}, fmt.Errorf("tool %q on server %q: invalid inputSchema: %w",
			tool.Name, serverName, err)
	}

	return llm.NewTool(name, description).
		WithSchema(schema).
		WithExecute(func(ctx context.Context, input json.RawMessage) ([]llm.ContentPart, error) {
			return executeMCPTool(ctx, manager, serverName, tool.Name, input)
		}).
		Build(), nil
}

// buildToolName creates the final tool name based on the naming strategy.
func buildToolName(serverName, toolName string, strategy ToolNamingStrategy) string {
	switch strategy {
	case ToolNamePrefix:
		// Sanitize names: replace spaces/non-alphanumeric with underscores.
		safeServer := sanitizeName(serverName)
		return safeServer + "_" + toolName
	default:
		return toolName
	}
}

// buildDescription formats the tool description including origin info
// and behavior annotations.
func buildDescription(serverName, description string, annotations *ToolAnnotations) string {
	var b strings.Builder
	if description == "" {
		b.WriteString(fmt.Sprintf("MCP tool from server %q", serverName))
	} else {
		b.WriteString(fmt.Sprintf("MCP tool from server %q: %s", serverName, description))
	}

	if hint := formatAnnotations(annotations); hint != "" {
		b.WriteString(" ")
		b.WriteString(hint)
	}

	return b.String()
}

// formatAnnotations returns a short bracketed hint string describing
// the tool's behavior annotations, or empty string if none are set.
// Examples: "[Read-only]" "[Destructive]" "[Idempotent]" "[Read-only, Idempotent]"
//
// Spec defaults (when pointer is nil):
//
//	readOnlyHint:    false — skip
//	destructiveHint: true  — include
//	idempotentHint:  false — skip
//	openWorldHint:   true  — include
func formatAnnotations(a *ToolAnnotations) string {
	if a == nil {
		return ""
	}

	var hints []string

	if a.ReadOnlyHint != nil && *a.ReadOnlyHint {
		hints = append(hints, "Read-only")
	}
	if a.DestructiveHint == nil || *a.DestructiveHint {
		hints = append(hints, "Destructive")
	}
	if a.IdempotentHint != nil && *a.IdempotentHint {
		hints = append(hints, "Idempotent")
	}
	if a.OpenWorldHint == nil || *a.OpenWorldHint {
		hints = append(hints, "Open-world")
	}

	if len(hints) == 0 {
		return ""
	}
	return "[" + strings.Join(hints, ", ") + "]"
}

// sanitizeName replaces characters that are problematic in tool names.
func sanitizeName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' || r == '.' || r == '/' || r == ':' {
			b.WriteRune('_')
		}
		// Skip other characters.
	}
	return b.String()
}

// executeMCPTool routes execution to the MCP server and converts results.
// Error wrapping is done by client.CallTool (which adds server + tool name),
// so this function passes through errors as-is.
func executeMCPTool(ctx context.Context, manager *Manager, serverName, toolName string, input json.RawMessage) ([]llm.ContentPart, error) {
	result, err := manager.CallTool(ctx, serverName, toolName, input)
	if err != nil {
		return nil, err // already wrapped by client.CallTool
	}

	// Convert MCP CallToolResult content to AlayaCore ContentParts.
	parts := make([]llm.ContentPart, 0, len(result.Content))
	for _, content := range result.Content {
		part := convertToolContent(content, serverName)
		if part != nil {
			parts = append(parts, part)
		}
	}

	return parts, nil
}

// convertToolContent converts a single MCP ToolContent to an AlayaCore ContentPart.
// Returns nil if the content cannot be converted.
//
//nolint:gocyclo // content type dispatch is inherently a switch; each case is simple.
func convertToolContent(content ToolContent, serverName string) llm.ContentPart {
	switch content.Type {
	case "text":
		return &llm.TextPart{Text: content.Text}

	case "image":
		return convertImageContent(content, serverName)

	case "audio":
		return convertAudioContent(content, serverName)

	case "resource":
		return convertResourceContent(content, serverName)

	case "resource_link":
		return convertResourceLinkContent(content, serverName)

	default:
		// Unknown content type — include as text if available.
		if content.Text != "" {
			return &llm.TextPart{Text: content.Text}
		}
		return nil
	}
}

// convertImageContent converts an image content part.
func convertImageContent(content ToolContent, serverName string) llm.ContentPart {
	if content.Data != "" && content.MIMEType != "" {
		dataURI := fmt.Sprintf("data:%s;base64,%s", content.MIMEType, content.Data)
		return &llm.ImagePart{URI: dataURI}
	}
	if content.Data != "" {
		return &llm.TextPart{
			Text: fmt.Sprintf("[Image from %s: %d bytes base64 data]",
				serverName, len(content.Data)),
		}
	}
	return nil
}

// convertAudioContent converts an audio content part.
func convertAudioContent(content ToolContent, serverName string) llm.ContentPart {
	if content.Data != "" && content.MIMEType != "" {
		dataURI := fmt.Sprintf("data:%s;base64,%s", content.MIMEType, content.Data)
		return &llm.AudioPart{URI: dataURI}
	}
	if content.Data != "" {
		return &llm.TextPart{
			Text: fmt.Sprintf("[Audio from %s: %d bytes base64 data]",
				serverName, len(content.Data)),
		}
	}
	return nil
}

// convertResourceContent converts an embedded resource content part.
func convertResourceContent(content ToolContent, serverName string) llm.ContentPart {
	if content.Resource == nil {
		return nil
	}
	return convertResourceContents(content.Resource, serverName)
}

// convertResourceContents converts a ResourceContents to an AlayaCore ContentPart.
func convertResourceContents(rc *ResourceContents, serverName string) llm.ContentPart {
	switch {
	case rc.Blob != "" && rc.MIMEType != "":
		// Text MIME → decode base64 and return as text.
		if strings.HasPrefix(rc.MIMEType, "text/") {
			if decoded, err := base64.StdEncoding.DecodeString(rc.Blob); err == nil {
				return &llm.TextPart{Text: string(decoded)}
			}
		}
		// Known media type (image, video, audio, etc.) → data URI.
		dataURI := fmt.Sprintf("data:%s;base64,%s", rc.MIMEType, rc.Blob)
		return llm.MediaContentPart(rc.MIMEType, dataURI)

	case rc.Blob != "":
		// Blob without MIME type.
		return &llm.TextPart{
			Text: fmt.Sprintf("[Resource from %s: %s (base64, %d bytes)]",
				serverName, rc.URI, len(rc.Blob)),
		}

	case rc.Text != "":
		// Text content.
		return &llm.TextPart{Text: rc.Text}

	case rc.URI != "":
		// URI reference only.
		return &llm.TextPart{
			Text: fmt.Sprintf("[Resource from %s: %s]", serverName, rc.URI),
		}

	default:
		return nil
	}
}

// convertResourceLinkContent converts a ResourceLink content to a ContentPart.
// ResourceLink is a reference to a resource without its content inline.
func convertResourceLinkContent(content ToolContent, serverName string) llm.ContentPart {
	if content.URI == "" {
		return nil
	}
	name := content.Name
	if name == "" {
		name = content.URI
	}
	label := fmt.Sprintf("[Resource from %s: %s]", serverName, name)
	if content.MIMEType != "" {
		label = fmt.Sprintf("[Resource from %s: %s (%s)]", serverName, name, content.MIMEType)
	}
	return &llm.TextPart{
		Text: fmt.Sprintf("%s\nURI: %s", label, content.URI),
	}
}

// newReadResourceTool creates a read_resource tool for a server that
// supports the Resource capability.
func newReadResourceTool(serverName string, manager *Manager) llm.Tool {
	name := buildToolName(serverName, "read_resource", ToolNamePrefix)
	description := fmt.Sprintf("Read a resource from MCP server %q by URI. "+
		"Available resource URIs are listed in the system prompt — refer to them above.", serverName)
	schema := json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string","description":"Resource URI to read"}},"required":["uri"]}`)

	return llm.NewTool(name, description).
		WithSchema(schema).
		WithExecute(func(ctx context.Context, input json.RawMessage) ([]llm.ContentPart, error) {
			var params struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			return executeReadResource(ctx, manager, serverName, params.URI)
		}).
		Build()
}

// executeReadResource reads a resource and converts the result to content parts.
func executeReadResource(ctx context.Context, manager *Manager, serverName, uri string) ([]llm.ContentPart, error) {
	result, err := manager.ReadResource(ctx, serverName, uri)
	if err != nil {
		return nil, err
	}

	parts := make([]llm.ContentPart, 0, len(result.Contents))
	for _, rc := range result.Contents {
		part := convertResourceContents(&rc, serverName)
		if part != nil {
			parts = append(parts, part)
		}
	}
	return parts, nil
}

// newGetPromptTool creates a get_prompt tool for a server that supports
// the Prompt capability.
func newGetPromptTool(serverName string, manager *Manager) llm.Tool {
	name := buildToolName(serverName, "get_prompt", ToolNamePrefix)
	description := fmt.Sprintf("Get a prompt from MCP server %q by name. "+
		"Prompts are templated message sequences that can be injected into the conversation. "+
		"Available prompt names are listed in the system prompt — refer to them above.", serverName)
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"name":{"type":"string","description":"Prompt name"},
			"arguments":{"type":"object","description":"Optional template arguments","additionalProperties":{"type":"string"}}
		},
		"required":["name"]
	}`)

	return llm.NewTool(name, description).
		WithSchema(schema).
		WithExecute(func(ctx context.Context, input json.RawMessage) ([]llm.ContentPart, error) {
			var params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			return executeGetPrompt(ctx, manager, serverName, params.Name, params.Arguments)
		}).
		Build()
}

// executeGetPrompt fetches a prompt and converts the messages to content parts.
func executeGetPrompt(ctx context.Context, manager *Manager, serverName, name string, args map[string]string) ([]llm.ContentPart, error) {
	result, err := manager.GetPrompt(ctx, serverName, name, args)
	if err != nil {
		return nil, err
	}

	var parts []llm.ContentPart
	if result.Description != "" {
		parts = append(parts, &llm.TextPart{Text: fmt.Sprintf("[Prompt: %s]", result.Description)})
	}
	for _, msg := range result.Messages {
		role := msg.Role
		content := convertToolContent(msg.Content, serverName)
		if content != nil {
			if role == "assistant" {
				parts = append(parts, &llm.TextPart{Text: "[Assistant]"}, content)
			} else {
				parts = append(parts, &llm.TextPart{Text: "[User]"}, content)
			}
		}
	}
	return parts, nil
}

// ParseServerConfig parses a single --mcp-server flag value.
// Format: name=value
//
// Supported formats:
//
//	name=https://example.com/mcp                    → Streamable HTTP
//	name=http://example.com/mcp                     → Streamable HTTP
//	name=exec:command arg1 arg2 ...                 → Stdio subprocess
//	name=exec:KEY=VALUE command arg1 arg2 ...       → Stdio with env vars
//
// Examples:
//
//	myapi=https://mcp.example.com
//	db=exec:npx @anthropic/mcp-db-server
//	db=exec:DB_HOST=localhost DB_PORT=5432 npx @anthropic/mcp-db-server
func ParseServerConfig(raw string) (ServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ServerConfig{}, fmt.Errorf("empty MCP server config")
	}

	idx := strings.Index(raw, "=")
	if idx <= 0 {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (expected name=value)", raw)
	}

	name := raw[:idx]
	rest := raw[idx+1:]
	if name == "" || rest == "" {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (name or value empty)", raw)
	}

	// exec: prefix → stdio transport
	if strings.HasPrefix(rest, "exec:") {
		return parseExecConfig(name, rest[len("exec:"):], raw)
	}

	// http:// or https:// prefix → Streamable HTTP transport
	if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
		return ServerConfig{
			Name: name,
			URL:  rest,
		}, nil
	}

	return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (expected https://URL or exec:command)", raw)
}

// parseExecConfig parses the value part of "exec:..." and extracts
// KEY=VALUE environment variables from the front of the command line.
func parseExecConfig(name, value, raw string) (ServerConfig, error) {
	parts := splitArgs(value)
	if len(parts) == 0 {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (empty command)", raw)
	}

	var env map[string]string
	cmdStart := 0
	for cmdStart < len(parts) {
		k, v, found := strings.Cut(parts[cmdStart], "=")
		if !found || k == "" || v == "" {
			break
		}
		if env == nil {
			env = make(map[string]string)
		}
		env[k] = v
		cmdStart++
	}
	if cmdStart >= len(parts) {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (no command after env vars)", raw)
	}

	return ServerConfig{
		Name:    name,
		Command: parts[cmdStart],
		Args:    parts[cmdStart+1:],
		Env:     env,
	}, nil
}

// splitArgs splits a command string into tokens, respecting quoted strings.
func splitArgs(input string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	var quoteChar byte

	for i := 0; i < len(input); i++ {
		c := input[i]
		switch {
		case inQuote:
			if c == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(c)
			}
		case c == '"' || c == '\'':
			inQuote = true
			quoteChar = c
		case c == ' ' || c == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// maxSchemaDepth is the maximum allowed nesting depth for an MCP tool's
// inputSchema. This prevents DoS attacks via deeply nested JSON Schema.
const maxSchemaDepth = 20

// sanitizeInputSchema validates and sanitizes an MCP tool's inputSchema
// before passing it to the LLM provider. Returns the original schema
// unchanged if valid, or an error if it violates security constraints.
//
// Security checks:
//   - Root must be a JSON object (not null, array, or primitive).
//   - No external $ref URIs (http/https) — prevents SSRF if the LLM
//     provider attempts to dereference them.
//   - Nesting depth limited to maxSchemaDepth — prevents DoS via
//     deeply nested schemas (anyOf/allOf/oneOf bombs).
func sanitizeInputSchema(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("schema is empty")
	}

	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Root must be an object.
	rootObj, ok := schema.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("root must be a JSON object, got %T", schema)
	}

	// Walk the schema tree checking for external $ref and depth.
	if err := walkSchema(rootObj, 0); err != nil {
		return nil, err
	}

	// Schema passes all checks — return it unchanged.
	return raw, nil
}

// walkSchema recursively walks a JSON Schema tree checking for:
//   - External $ref values (http/https)
//   - Nesting depth exceeding maxSchemaDepth
//
// It returns an error if any constraint is violated.
func walkSchema(node map[string]any, depth int) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("schema nesting exceeds maximum depth of %d", maxSchemaDepth)
	}

	// Check for external $ref.
	if ref, ok := node["$ref"]; ok {
		refStr, ok := ref.(string)
		if ok && (len(refStr) > 0) {
			if strings.HasPrefix(refStr, "http://") || strings.HasPrefix(refStr, "https://") {
				return fmt.Errorf("external $ref not allowed: %q", refStr)
			}
		}
	}

	// Recurse into all sub-objects and arrays of objects.
	for _, val := range node {
		switch v := val.(type) {
		case map[string]any:
			if err := walkSchema(v, depth+1); err != nil {
				return err
			}
		case []any:
			for _, item := range v {
				if itemObj, ok := item.(map[string]any); ok {
					if err := walkSchema(itemObj, depth+1); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}
