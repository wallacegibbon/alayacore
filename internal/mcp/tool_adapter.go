package mcp

import (
	"context"
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

func toolsToAgentTools(serverTools map[string][]Tool, manager *Manager, strategy ToolNamingStrategy) []llm.Tool {
	var result []llm.Tool

	for serverName, tools := range serverTools {
		for _, tool := range tools {
			adapted := adaptTool(serverName, tool, manager, strategy)
			result = append(result, adapted)
		}
	}

	return result
}

// adaptTool converts a single MCP tool to an llm.Tool.
func adaptTool(serverName string, tool Tool, manager *Manager, strategy ToolNamingStrategy) llm.Tool {
	name := buildToolName(serverName, tool.Name, strategy)
	description := buildDescription(serverName, tool.Description, tool.Annotations)

	// MCP inputSchema is already a valid JSON Schema object.
	// We pass it directly to the tool definition.
	schema := tool.InputSchema

	return llm.NewTool(name, description).
		WithSchema(schema).
		WithExecute(func(ctx context.Context, input json.RawMessage) ([]llm.ContentPart, error) {
			return executeMCPTool(ctx, manager, serverName, tool.Name, input)
		}).
		Build()
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
	rc := content.Resource

	switch {
	case rc.Blob != "" && rc.MIMEType != "":
		// Base64 blob with known MIME type — embed as data URI.
		dataURI := fmt.Sprintf("data:%s;base64,%s", rc.MIMEType, rc.Blob)
		switch {
		case strings.HasPrefix(rc.MIMEType, "image/"):
			return &llm.ImagePart{URI: dataURI}
		case strings.HasPrefix(rc.MIMEType, "video/"):
			return &llm.VideoPart{URI: dataURI}
		case strings.HasPrefix(rc.MIMEType, "audio/"):
			return &llm.AudioPart{URI: dataURI}
		default:
			return &llm.DocumentPart{URI: dataURI}
		}

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

// ParseServerConfig parses a single --mcp-server flag value.
// Format: name=transport:value
//
// Supported transports:
//
//	exec — stdio subprocess, value is command line
//	sse  — legacy HTTP+SSE transport, value is URL
//	http — Streamable HTTP transport, value is URL
//
// Examples:
//
//	db=exec:npx @anthropic/mcp-db-server
//	remote=sse:https://example.com/sse
//	remote=http:https://example.com/mcp
func ParseServerConfig(raw string) (ServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ServerConfig{}, fmt.Errorf("empty MCP server config")
	}

	idx := strings.Index(raw, "=")
	if idx <= 0 {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (expected name=transport:value)", raw)
	}

	name := raw[:idx]
	rest := raw[idx+1:]
	if name == "" || rest == "" {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (name or value empty)", raw)
	}

	transport, value, ok := parseTransportPrefix(rest)
	if !ok {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (expected transport:value, where transport is exec, sse, or http)", raw)
	}

	switch transport {
	case "exec":
		parts := splitArgs(value)
		if len(parts) == 0 {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (empty command)", raw)
		}
		return ServerConfig{
			Name:    name,
			Command: parts[0],
			Args:    parts[1:],
		}, nil

	case "sse":
		if value == "" {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (empty URL)", raw)
		}
		return ServerConfig{
			Name:          name,
			URL:           value,
			TransportType: TransportSSE,
		}, nil

	case "http":
		if value == "" {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (empty URL)", raw)
		}
		return ServerConfig{
			Name:          name,
			URL:           value,
			TransportType: TransportStreamable,
		}, nil

	default:
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q: unknown transport %q", raw, transport)
	}
}

// parseTransportPrefix checks if s starts with a known transport prefix
// followed by ":". Returns the transport name, the rest, and true if matched.
func parseTransportPrefix(s string) (transport, value string, ok bool) {
	known := []string{"exec", "sse", "http"}
	for _, t := range known {
		if strings.HasPrefix(s, t+":") {
			return t, s[len(t)+1:], true
		}
	}
	return "", "", false
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
