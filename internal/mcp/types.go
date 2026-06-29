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
	"fmt"
)

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

// ImplementationLevel indicates the MCP specification level.
type ImplementationLevel string

const (
	LevelClient ImplementationLevel = "client"
	LevelServer ImplementationLevel = "server"
)

// ClientCapabilities describes the capabilities the client supports.
type ClientCapabilities struct {
	// Experimental non-standard capabilities.
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
	// Roots is optional root resource support.
	Roots *struct{} `json:"roots,omitempty"`
	// Sampling is optional LLM sampling support.
	Sampling *struct{} `json:"sampling,omitempty"`
}

// ServerCapabilities describes the capabilities the server supports.
type ServerCapabilities struct {
	// Experimental non-standard capabilities.
	Experimental map[string]json.RawMessage `json:"experimental,omitempty"`
	// Logging is optional logging support.
	Logging *struct{} `json:"logging,omitempty"`
	// Prompts is optional prompt template support.
	Prompts *struct{} `json:"prompts,omitempty"`
	// Resources is optional resource support.
	Resources *struct{} `json:"resources,omitempty"`
	// Tools is optional tool support.
	Tools *struct{} `json:"tools,omitempty"`
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
}

// ImplementationInfo describes the name and version of the implementation.
type ImplementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool represents a tool exposed by an MCP server.
// This is the response type for tools/list.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ListToolsResult is the result of the "tools/list" method.
type ListToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// CallToolRequest is the params for the "tools/call" method.
type CallToolRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the result of the "tools/call" method.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent represents a piece of content in a tool call result.
type ToolContent struct {
	Type string `json:"type"`
	// Text is used for type "text".
	Text string `json:"text,omitempty"`
	// URI/MIMEType are used for type "resource".
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	// Blob is base64-encoded binary data for type "resource".
	Blob string `json:"blob,omitempty"`
}

// MCP protocol version constant.
const protocolVersion = "2025-03-26"

// Method names.
const (
	methodInitialize               = "initialize"
	methodListTools                = "tools/list"
	methodCallTool                 = "tools/call"
	methodListResources            = "resources/list"
	methodReadResource             = "resources/read"
	methodListPrompts              = "prompts/list"
	methodGetPrompt                = "prompts/get"
	methodPing                     = "ping"
	methodNotificationsInitialized = "notifications/initialized"
)

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

	// URL is the SSE endpoint URL for HTTP transport.
	// If URL is set, Command must be empty.
	URL string

	// Env is additional environment variables for stdio transport.
	Env map[string]string

	// Debug enables logging of raw JSON-RPC messages to a file.
	Debug bool
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
