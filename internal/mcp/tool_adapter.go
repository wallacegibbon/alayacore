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
// Unified format: name=transport:value
//
// Examples:
//
//	name=exec:command arg1 arg2      — stdio transport
//	name=sse:url                     — legacy SSE transport
//	name=http:url                    — Streamable HTTP transport
//	name=url                         — HTTP auto-detect (no transport prefix)
//	name=command arg1 arg2           — stdio (backwards compat)
//	name@url                         — HTTP auto-detect (backwards compat)
//	name@sse+url                     — explicit legacy SSE (backwards compat)
//	name@streamable+url              — explicit Streamable HTTP (backwards compat)
func ParseServerConfig(raw string) (ServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ServerConfig{}, fmt.Errorf("empty MCP server config")
	}

	// Check for '=' before '@' because command args may contain '@'
	// (e.g. "db=npx @anthropic/mcp-db-server").
	if idx := strings.Index(raw, "="); idx > 0 {
		name := raw[:idx]
		rest := raw[idx+1:]
		if name == "" || rest == "" {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (name or value empty)", raw)
		}
		return parseEqFormat(name, rest)
	}

	// name@url format (HTTP only, backwards compatible).
	if idx := strings.Index(raw, "@"); idx > 0 {
		name := raw[:idx]
		rest := raw[idx+1:]
		if name == "" || rest == "" {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (name or URL empty)", raw)
		}
		cfg := ServerConfig{Name: name}
		cfg.URL, cfg.TransportType = parseTransportPrefix(rest)
		return cfg, nil
	}

	return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (expected name=value or name@url)", raw)
}

// parseEqFormat parses the "name=value" format.
// The value can be:
//   - transport:value  (e.g. "exec:node server.js", "sse:url", "http:url")
//   - command args     (backwards compatible stdio, no recognized prefix)
func parseEqFormat(name, rest string) (ServerConfig, error) {
	// Check for known transport: prefix.
	if transport, value, ok := parseTransportColon(rest); ok {
		switch transport {
		case "exec":
			parts := splitArgs(value)
			if len(parts) == 0 {
				return ServerConfig{}, fmt.Errorf("invalid MCP server config %q: no command after exec: prefix", rest)
			}
			return ServerConfig{
				Name:    name,
				Command: parts[0],
				Args:    parts[1:],
			}, nil
		case "sse":
			return ServerConfig{
				Name:          name,
				URL:           value,
				TransportType: TransportSSE,
			}, nil
		case "http":
			return ServerConfig{
				Name:          name,
				URL:           value,
				TransportType: TransportStreamable,
			}, nil
		default:
			return ServerConfig{}, fmt.Errorf("invalid MCP server config %q: unknown transport %q", rest, transport)
		}
	}

	// No transport prefix — treat as stdio (backwards compatible).
	parts := splitArgs(rest)
	if len(parts) == 0 {
		return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (no command)", rest)
	}
	return ServerConfig{
		Name:    name,
		Command: parts[0],
		Args:    parts[1:],
	}, nil
}

// parseTransportColon checks if s starts with a known transport prefix
// followed by ":". Returns the transport name, the rest, and true if matched.
// Known transports: exec, sse, http.
func parseTransportColon(s string) (transport, value string, ok bool) {
	known := []string{"exec", "sse", "http"}
	for _, t := range known {
		if strings.HasPrefix(s, t+":") {
			return t, s[len(t)+1:], true
		}
	}
	return "", "", false
}

// parseTransportPrefix checks for explicit transport prefix ("sse+" or
// "streamable+") before the URL (used in name@url format).
func parseTransportPrefix(raw string) (url string, transport string) {
	if strings.HasPrefix(raw, "streamable+") {
		return raw[len("streamable+"):], TransportStreamable
	}
	if strings.HasPrefix(raw, "sse+") {
		return raw[len("sse+"):], TransportSSE
	}
	return raw, TransportAuto
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
