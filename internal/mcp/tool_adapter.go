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
	description := buildDescription(serverName, tool.Description)

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

// buildDescription formats the tool description including origin info.
func buildDescription(serverName, description string) string {
	if description == "" {
		return fmt.Sprintf("MCP tool from server %q", serverName)
	}
	return fmt.Sprintf("MCP tool from server %q: %s", serverName, description)
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
		switch content.Type {
		case "text":
			parts = append(parts, &llm.TextPart{Text: content.Text})

		case "resource":
			// Resource with base64 blob — embed as data URI so the LLM
			// receives the actual content (same pattern as read_file).
			switch {
			case content.Blob != "" && content.MIMEType != "":
				dataURI := fmt.Sprintf("data:%s;base64,%s", content.MIMEType, content.Blob)
				switch {
				case strings.HasPrefix(content.MIMEType, "image/"):
					parts = append(parts, &llm.ImagePart{URI: dataURI})
				case strings.HasPrefix(content.MIMEType, "video/"):
					parts = append(parts, &llm.VideoPart{URI: dataURI})
				case strings.HasPrefix(content.MIMEType, "audio/"):
					parts = append(parts, &llm.AudioPart{URI: dataURI})
				default:
					// Document or unknown type — use DocumentPart.
					parts = append(parts, &llm.DocumentPart{URI: dataURI})
				}
			case content.Blob != "":
				// Blob without MIME type — wrap with metadata.
				parts = append(parts, &llm.TextPart{
					Text: fmt.Sprintf("[Resource from %s: %s (base64, %d bytes)]",
						serverName, content.URI, len(content.Blob)),
				})
			case content.URI != "":
				parts = append(parts, &llm.TextPart{
					Text: fmt.Sprintf("[Resource from %s: %s]", serverName, content.URI),
				})
			}

		default:
			// Unknown content type — include as text if available.
			if content.Text != "" {
				parts = append(parts, &llm.TextPart{Text: content.Text})
			}
		}
	}

	return parts, nil
}

// ServerFromToolName attempts to extract the server name from a prefixed
// tool name by splitting on the last underscore.
//
// This is best-effort only: it fails when the tool name itself contains
// underscores (e.g. "my_db_query_result" is ambiguous). Production code
// does NOT use this function — tool routing goes through the closure
// created by adaptTool, which captures the correct server+tool pair.
//
// Deprecated: do not use for runtime routing. Only exists for diagnostic
// display purposes.
//
//	"my_server_query"  → "my_server", "query", true
//	"bare_tool"        → "", "", false  (no prefix separator found)
func ServerFromToolName(prefixedName string) (server, tool string, ok bool) {
	idx := strings.LastIndex(prefixedName, "_")
	if idx <= 0 || idx >= len(prefixedName)-1 {
		return "", "", false
	}
	return prefixedName[:idx], prefixedName[idx+1:], true
}

// ParseServerConfig parses a single --mcp-server flag value.
// Supported formats:
//
//	"name=command arg1 arg2"    — stdio transport
//	"name@url"                 — SSE transport
func ParseServerConfig(raw string) (ServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ServerConfig{}, fmt.Errorf("empty MCP server config")
	}

	// Try stdio format first: name=command arg1 arg2
	// Check for '=' before '@' because command args may contain '@'
	// (e.g. "db=npx @anthropic/mcp-db-server").
	if idx := strings.Index(raw, "="); idx > 0 {
		name := raw[:idx]
		rest := raw[idx+1:]
		if name == "" || rest == "" {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (name or command empty)", raw)
		}

		parts := splitArgs(rest)
		if len(parts) == 0 {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (no command)", raw)
		}

		return ServerConfig{
			Name:    name,
			Command: parts[0],
			Args:    parts[1:],
		}, nil
	}

	// Try SSE format: name@url
	if idx := strings.Index(raw, "@"); idx > 0 {
		name := raw[:idx]
		url := raw[idx+1:]
		if name == "" || url == "" {
			return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (name or URL empty)", raw)
		}
		return ServerConfig{Name: name, URL: url}, nil
	}

	return ServerConfig{}, fmt.Errorf("invalid MCP server config: %q (expected name=command or name@url)", raw)
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
