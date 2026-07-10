// Package mcp implements a Model Context Protocol (MCP) client
// for discovering and using tools from MCP servers.
//
// MCP is an open standard (github.com/modelcontextprotocol) that
// defines a JSON-RPC 2.0 based protocol for LLM tool/resource
// interaction. This package implements the client side — AlayaCore
// acts as an MCP host, connecting to external MCP servers to
// extend the agent's capabilities.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/mcp/auth"
)

// ErrNeedsAuth is returned by Connect() when the server requires
// interactive OAuth authorization (authorization_code flow).
var ErrNeedsAuth = errors.New("mcp server needs interactive authorization")

// ============================================================================
// JSON-RPC 2.0 Base Types
// ============================================================================

// jsonrpcVersion is the JSON-RPC protocol version string.
const jsonrpcVersion = "2.0"

// requestID is a JSON-RPC request identifier that accepts both string and
// number IDs from JSON for spec compatibility (JSON-RPC 2.0 allows both).
// Internally it is stored as a string for uniform comparison in dispatch maps.
type requestID string

// UnmarshalJSON accepts both JSON string and number as request ID.
func (id *requestID) UnmarshalJSON(data []byte) error {
	// Try string first (most MCP SDKs use string IDs).
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*id = requestID(s)
		return nil
	}

	// Fall back to number.
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("requestID: cannot unmarshal %s", string(data))
	}
	*id = requestID(n.String())
	return nil
}

// MarshalJSON returns the request ID as a JSON string.
func (id requestID) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(id))
}

// jsonrpcRequest is a JSON-RPC 2.0 request object.
// ID is omitted for notifications (zero value = empty string).
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      requestID       `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response object.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      requestID       `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// jsonrpcError represents a JSON-RPC error object.
type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ============================================================================
// MCP Protocol Types (subset needed for tool support)
// ============================================================================

// Meta is an optional metadata object for experimental features, as defined by
// the MCP spec (_meta?: { [key: string]: unknown }). It carries
// implementation-specific data that parties may use to extend the protocol
// without waiting for the specification to add new fields.
type Meta map[string]json.RawMessage

// ClientCapabilities describes the capabilities the client supports.
type ClientCapabilities struct {
	// Experimental non-standard capabilities.
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
	// Roots is optional root resource support.
	Roots *ClientRootCapabilities `json:"roots,omitempty"`
	// Sampling is optional LLM sampling support.
	Sampling *ClientSamplingCapabilities `json:"sampling,omitempty"`
	// Elicitation is optional server-elicitation support.
	Elicitation *ClientElicitationCapabilities `json:"elicitation,omitempty"`
}

// ClientRootCapabilities describes the client's root resource capabilities.
type ClientRootCapabilities struct {
	// ListChanged indicates whether the client supports notifications for
	// changes to the roots list.
	ListChanged bool `json:"listChanged,omitempty"`
}

// ClientSamplingCapabilities describes the client's LLM sampling capabilities.
type ClientSamplingCapabilities struct {
	// Context indicates whether the client supports context inclusion
	// via includeContext parameter.
	Context *struct{} `json:"context,omitempty"`
	// Tools indicates whether the client supports tool use via tools and
	// toolChoice parameters.
	Tools *struct{} `json:"tools,omitempty"`
}

// ClientElicitationCapabilities describes the client's elicitation capabilities.
type ClientElicitationCapabilities struct {
	Form *struct{} `json:"form,omitempty"`
	URL  *struct{} `json:"url,omitempty"`
}

// ServerCapabilities describes the capabilities the server supports.
type ServerCapabilities struct {
	// Experimental non-standard capabilities.
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
	// Logging is optional logging support.
	Logging *struct{} `json:"logging,omitempty"`
	// Completions is optional argument autocompletion support.
	Completions *struct{} `json:"completions,omitempty"`
	// Prompts is optional prompt template support.
	Prompts *ServerPromptCapabilities `json:"prompts,omitempty"`
	// Resources is optional resource support.
	Resources *ServerResourceCapabilities `json:"resources,omitempty"`
	// Tools is optional tool support.
	Tools *ServerToolCapabilities `json:"tools,omitempty"`
}

// ServerPromptCapabilities describes the server's prompt capabilities.
type ServerPromptCapabilities struct {
	// ListChanged indicates whether the server supports notifications for
	// changes to the prompt list.
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerResourceCapabilities describes the server's resource capabilities.
type ServerResourceCapabilities struct {
	// Subscribe indicates whether the server supports subscribing to
	// resource updates.
	Subscribe bool `json:"subscribe,omitempty"`
	// ListChanged indicates whether the server supports notifications for
	// changes to the resource list.
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerToolCapabilities describes the server's tool capabilities.
type ServerToolCapabilities struct {
	// ListChanged indicates whether the server supports notifications for
	// changes to the tool list.
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeRequest is the params for the "initialize" method.
type InitializeRequest struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ImplementationInfo `json:"clientInfo"`
}

// InitializeResult is the result of the "initialize" method.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ImplementationInfo `json:"serverInfo"`
	// Instructions describing how to use the server and its features.
	// This can be used by clients to improve the LLM's understanding of
	// available tools, resources, etc.
	Instructions string `json:"instructions,omitempty"`
	Meta         Meta   `json:"_meta,omitempty"`
}

// ImplementationInfo describes the name and version of the implementation.
type ImplementationInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"websiteUrl,omitempty"`
	Icons       []Icon `json:"icons,omitempty"`
}

// Icon represents an optional icon for tools, resources, or prompts.
type Icon struct {
	Src      string   `json:"src"`
	MIMEType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    string   `json:"theme,omitempty"`
}

// ToolExecution describes execution options for a tool.
type ToolExecution struct {
	// TaskSupport indicates whether this tool supports task-augmented execution.
	// "forbidden" (default), "optional", or "required".
	TaskSupport string `json:"taskSupport,omitempty"`
}

// Tool represents a tool exposed by an MCP server.
// This is the response type for tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Annotations are optional hints about the tool's behavior.
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	// Icon metadata for the tool.
	Icons []Icon `json:"icons,omitempty"`
	// Execution options.
	Execution *ToolExecution `json:"execution,omitempty"`
	// Optional output schema for structured content.
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	// Meta is an optional metadata object for experimental features.
	Meta Meta `json:"_meta,omitempty"`
}

// ToolAnnotations provides optional hints about a tool to clients.
// NOTE: all properties are hints — they are not guaranteed to provide
// a faithful description of tool behavior.
//
// Spec defaults (when pointer is nil/feld absent):
//
//	ReadOnlyHint:    false
//	DestructiveHint: true
//	IdempotentHint:  false
//	OpenWorldHint:   true
type ToolAnnotations struct {
	// A human-readable title for the tool.
	Title string `json:"title,omitempty"`
	// If true, the tool does not modify its environment. Default: false.
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
	// If true, the tool may perform destructive updates. Default: true.
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	// If true, calling the tool repeatedly with the same arguments has no
	// additional effect. Default: false.
	IdempotentHint *bool `json:"idempotentHint,omitempty"`
	// If true, this tool may interact with an "open world" of external
	// entities. Default: true.
	OpenWorldHint *bool `json:"openWorldHint,omitempty"`
}

// ListToolsResult is the result of the "tools/list" method.
type ListToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
	Meta       Meta   `json:"_meta,omitempty"`
}

// CallToolRequest is the params for the "tools/call" method.
type CallToolRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the result of the "tools/call" method.
type CallToolResult struct {
	Content    []ToolContent  `json:"content"`
	Structured map[string]any `json:"structuredContent,omitempty"`
	IsError    bool           `json:"isError,omitempty"`
	Meta       Meta           `json:"_meta,omitempty"`
}

// ToolContent represents a piece of content in a tool call result.
//
// The MCP spec defines four content types:
//
//	text:     {"type":"text", "text":"..."}
//	image:    {"type":"image", "data":"base64...", "mimeType":"..."}
//	audio:    {"type":"audio", "data":"base64...", "mimeType":"..."}
//	resource: {"type":"resource", "resource":{"uri":"...","mimeType":"...","text|blob":"..."}}
type ToolContent struct {
	Type string `json:"type"`
	// Text is used for type "text".
	Text string `json:"text,omitempty"`
	// Data is base64-encoded binary data for types "image" and "audio".
	Data string `json:"data,omitempty"`
	// MIMEType describes the media type for "image", "audio", "resource_link", and "resource".
	MIMEType string `json:"mimeType,omitempty"`
	// URI is used for type "resource_link".
	URI string `json:"uri,omitempty"`
	// Name is used for type "resource_link".
	Name string `json:"name,omitempty"`
	// Resource is used for type "resource" — a reference to a resource
	// with its contents embedded inline.
	Resource *ResourceContents `json:"resource,omitempty"`
}

// Resource represents a resource exposed by an MCP server.
type Resource struct {
	URI         string       `json:"uri"`
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	MIMEType    string       `json:"mimeType,omitempty"`
	Annotations *Annotations `json:"annotations,omitempty"`
	Icons       []Icon       `json:"icons,omitempty"`
	Size        *float64     `json:"size,omitempty"`
	Meta        Meta         `json:"_meta,omitempty"`
}

// Annotations represents optional metadata on resources and content items.
type Annotations struct {
	Audience     []Role  `json:"audience,omitempty"`     // "user" or "assistant"
	Priority     float64 `json:"priority,omitempty"`     // 0.0 – 1.0
	LastModified string  `json:"lastModified,omitempty"` // ISO 8601 formatted string
}

// ListResourcesResult is the result of the "resources/list" method.
type ListResourcesResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
	Meta       Meta       `json:"_meta,omitempty"`
}

// ReadResourceRequest is the params for the "resources/read" method.
type ReadResourceRequest struct {
	URI string `json:"uri"`
}

// ReadResourceResult is the result of the "resources/read" method.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
	Meta     Meta               `json:"_meta,omitempty"`
}

// Prompt represents a prompt or prompt template exposed by an MCP server.
type Prompt struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
	Icons       []Icon           `json:"icons,omitempty"`
	Meta        Meta             `json:"_meta,omitempty"`
}

// PromptArgument describes an argument a prompt template accepts.
type PromptArgument struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ListPromptsResult is the result of the "prompts/list" method.
type ListPromptsResult struct {
	Prompts    []Prompt `json:"prompts"`
	NextCursor string   `json:"nextCursor,omitempty"`
	Meta       Meta     `json:"_meta,omitempty"`
}

// GetPromptRequest is the params for the "prompts/get" method.
type GetPromptRequest struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// GetPromptResult is the result of the "prompts/get" method.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
	Meta        Meta            `json:"_meta,omitempty"`
}

// PromptMessage is a single message in a prompt result.
type PromptMessage struct {
	Role    Role        `json:"role"` // "user" or "assistant"
	Content ToolContent `json:"content"`
}

// Role represents the sender or recipient of a message in a conversation.
// Per the MCP spec, the only valid values are "user" and "assistant".
type Role string

const (
	// RoleUser represents a user message.
	RoleUser Role = "user"
	// RoleAssistant represents an assistant message.
	RoleAssistant Role = "assistant"
)

// ResourceContents represents the contents of a resource embedded in a
// tool result or prompt message, per the MCP spec.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	// Text is the text content (mutually exclusive with Blob).
	Text string `json:"text,omitempty"`
	// Blob is base64-encoded binary data (mutually exclusive with Text).
	Blob string `json:"blob,omitempty"`
}

// MCP protocol version constant.
const protocolVersion = "2025-11-25"

// Method names.
const (
	methodInitialize                        = "initialize"
	methodListTools                         = "tools/list"
	methodCallTool                          = "tools/call"
	methodListResources                     = "resources/list"
	methodReadResource                      = "resources/read"
	methodListPrompts                       = "prompts/list"
	methodGetPrompt                         = "prompts/get"
	methodPing                              = "ping"
	methodNotificationsInitialized          = "notifications/initialized"
	methodNotificationsCanceled             = "notifications/cancelled" //nolint:misspell // MCP spec method name
	methodNotificationsToolsListChanged     = "notifications/tools/list_changed"
	methodNotificationsResourcesListChanged = "notifications/resources/list_changed"
	methodNotificationsPromptsListChanged   = "notifications/prompts/list_changed"
)

// CanceledNotificationParams is sent by the client to inform the server
// that a previously-issued request is canceled. The server SHOULD stop
// processing and return an error with code -32800 (Request Canceled).
type CanceledNotificationParams struct {
	RequestID requestID `json:"requestId"`
	Reason    string    `json:"reason,omitempty"`
}

// ServerConfig describes how to connect to an MCP server.
type ServerConfig struct {
	// Name is a human-readable identifier for this server.
	// Used for logging and tool name prefixing.
	Name string

	// Command is the executable path for stdio transport.
	// If Command is set, URL must be empty.
	Command string

	// Args are the command-line arguments for stdio transport.
	Args []string

	// URL is the MCP endpoint URL for HTTP transport.
	// If URL is set, Command must be empty.
	URL string

	// Env is additional environment variables for stdio transport.
	Env map[string]string

	// Auth configures OAuth authentication for this server.
	Auth *AuthConfig

	// TokenStore is used to persist and load OAuth tokens to/from disk.
	// If nil, tokens are kept only in memory (lost on restart).
	TokenStore auth.TokenStore

	// Debug enables logging of raw JSON-RPC messages to a file.
	Debug bool
}

// AuthType enumerates the supported OAuth authentication modes.
type AuthType string

const (
	AuthTypeNone              AuthType = ""
	AuthTypeStatic            AuthType = "static"
	AuthTypeAuthorizationCode AuthType = "authorization_code"
)

// AuthConfig configures OAuth authentication for an MCP server.
type AuthConfig struct {
	// Type selects the authentication mode.
	Type AuthType

	// TokenEndpoint is the OAuth token endpoint URL.
	// If empty, it may be discovered from the authorization server metadata.
	TokenEndpoint string

	// ClientID is the OAuth client identifier.
	ClientID string

	// ClientSecret is the OAuth client secret (for authorization_code).
	ClientSecret string

	// ClientAuthMethod is the OAuth client authentication method for token
	// endpoint requests. Values: "client_secret_basic" (default/recommended)
	// or "client_secret_post". If empty, defaults to "client_secret_basic".
	ClientAuthMethod string

	// Scopes is the list of OAuth scopes to request.
	Scopes []string

	// Token is a pre-obtained access token (for static auth).
	Token string

	// obtainedToken is set by AuthorizeServer after the interactive
	// authorization_code flow completes. It is not persisted.
	obtainedToken *auth.Token
}

// ServerConfigFile is the on-disk structure for a single MCP server
// configuration block in mcp.conf. It uses config tags for the
// alayacore key-value config parser.
type ServerConfigFile struct {
	Server  string            `config:"server"`
	URL     string            `config:"url"`
	Command string            `config:"command"`
	Args    []string          `config:"args"`
	Env     map[string]string `config:"env"`

	AuthType         string   `config:"auth-type"`
	AuthScopes       []string `config:"auth-scopes"`
	AuthToken        string   `config:"auth-token"`
	AuthClientID     string   `config:"auth-client-id"`
	AuthClientSecret string   `config:"auth-client-secret"`
}

// RPCError represents a JSON-RPC error response.
type RPCError struct {
	Code    int
	Message string
	Data    any
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("MCP RPC error %d: %s", e.Code, e.Message)
}

// ToServerConfig converts a parsed config file entry to a ServerConfig.
func (f *ServerConfigFile) ToServerConfig() ServerConfig {
	cfg := ServerConfig{
		Name:    f.Server,
		URL:     f.URL,
		Command: f.Command,
		Args:    f.Args,
		Env:     f.Env,
	}

	if f.AuthType != "" {
		cfg.Auth = &AuthConfig{
			Type:         AuthType(f.AuthType),
			ClientID:     f.AuthClientID,
			ClientSecret: f.AuthClientSecret,
			Scopes:       f.AuthScopes,
			Token:        f.AuthToken,
		}
	}

	return cfg
}

// ============================================================================
// Configuration Parsing
// ============================================================================

// ParseServerConfig parses a string in the form "name=https://..." or
// "name=exec:command" into a ServerConfig.
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
