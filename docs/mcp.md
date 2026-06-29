# MCP (Model Context Protocol)

AlayaCore can connect to external **MCP servers** to extend the agent's
toolset. MCP (Model Context Protocol) is an open standard that defines
how LLMs discover and invoke tools from external services — databases,
APIs, web scraping, code analysis, and more.

When MCP servers are configured, their tools are automatically discovered
and merged with AlayaCore's built-in tools (`read_file`, `write_file`,
`execute_command`, etc.). The agent can use all of them transparently
in the same tool-calling loop.

## Quick Start

```bash
# Connect to an MCP server via stdio
alayacore --mcp-server "db=npx @anthropic/mcp-db-server"

# Multiple servers
alayacore --mcp-server "db=npx @anthropic/mcp-db-server" \
          --mcp-server "git=uvx mcp-git"
```

## CLI Flags

| Flag | Description |
|------|-------------|
| `--mcp-server` | MCP server config. Can be specified multiple times. |
| `--debug-mcp` | Log raw JSON-RPC messages to `alayacore-debug-mcp-N.log` |

## `--mcp-server` Format

Two formats are supported:

### Stdio Transport

```
name=command arg1 arg2 ...
```

The name is used for tool name prefixing and logging. The command is
executed as a subprocess; JSON-RPC messages are sent/received over
stdin/stdout as newline-delimited JSON (NDJSON).

```bash
# Simple command
--mcp-server "db=npx @anthropic/mcp-db-server"

# Command with arguments
--mcp-server "search=python /path/to/server.py --port 8080"

# Commands with quoted arguments
--mcp-server "git=uvx mcp-git --repo /path/to/repo"
```

### SSE Transport

```
name@url
```

Connects to an MCP server over Server-Sent Events (HTTP).

```bash
--mcp-server "remote@https://mcp.example.com/sse"
```

> **Note**: SSE transport is fully implemented. The client connects via
> HTTP GET to the SSE endpoint, receives the POST endpoint URL from the
> server's `endpoint` event, and sends JSON-RPC requests as HTTP POST
> with responses arriving as SSE `message` events.

## Tool Naming

MCP tools are prefixed with the server name to avoid conflicts with
built-in tools and between servers:

```
<server>_<tool>
```

For example, with `--mcp-server "db=npx @anthropic/mcp-db-server"`:

| Original tool name | Prefixed name | Description |
|--------------------|---------------|-------------|
| `query` | `db_query` | Run SQL query |
| `list_tables` | `db_list_tables` | List database tables |

The prefixed name is set as the tool's `name` in the API `tools` parameter,
so the LLM always uses it when making tool calls.

## How It Works

```
Agent Loop
  │
  ├── Built-in tools (read_file, write_file, ...)
  │
  └── MCP tools (db_query, git_status, ...)
        │
        ▼
    Manager
      ├── Client "db"  ─── StdioTransport ─── npx @anthropic/mcp-db-server
      └── Client "api" ─── SSETransport  ─── https://api.example.com/mcp/sse
```

1. **Startup**: AlayaCore spawns each MCP server as a child process
   (or connects via SSE), performs the MCP initialize handshake, and
   calls `tools/list` to discover available tools.

2. **Tool registration**: Each discovered tool is wrapped as an
   `llm.Tool` with the prefixed name and wired to route calls back
   to the originating server.

3. **Tool execution**: When the agent calls a tool, the request is
   sent as a JSON-RPC `tools/call` message to the corresponding
   server. The response is converted back into AlayaCore's content
   format and returned to the agent.

4. **Cancellation**: If the user cancels a task while waiting for an
   MCP tool response, the pending request is unregistered and a
   `notifications/cancelled` notification is sent to the server
   (best-effort) so it can abort processing early. Whether or not
   the server receives the notification, any late response from the
   server is discarded.

5. **Server crash**: If an MCP server process exits unexpectedly, a
   monitor goroutine detects the death via the transport's Done channel,
   transitions the client to a failed state, and signals via the client's
   Done channel. Subsequent tool calls to that server return a clear
   error: `mcp client "name": server connection lost`. The agent
   handles this like any other tool failure.

## Debugging

Use `--debug-mcp` to log all JSON-RPC messages:

```bash
alayacore --debug-mcp --mcp-server "db=npx @anthropic/mcp-db-server"
```

This creates `alayacore-debug-mcp-0.log` in the current directory:

```
MCP debug log started for: npx @anthropic/mcp-db-server
>>> initialize {"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"alayacore","version":"0.1.0"}}
<<< {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"mcp-db","version":"1.0.0"}}}
>>> notifications/initialized
>>> tools/list
<<< {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"query","description":"Run SQL query","inputSchema":{"type":"object"}}]}}
>>> tools/call {"name":"query","arguments":{"sql":"SELECT * FROM users"}}
<<< {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"..."}]}}
```

`>>>` marks requests sent to the server, `<<<` marks responses.
Notifications (like `notifications/initialized`) have no response.
JSON is pretty-printed for readability.

## Error Handling

- **Connection failures**: If an MCP server cannot be started or
  initialized, an error message is displayed via the adapter and the
  server is skipped. Other servers are unaffected.

- **Tool call failures**: If a tool call returns an error (JSON-RPC
  error or `isError: true`), the error message is included in the
  tool result and returned to the agent like any other tool error.

- **Server crashes**: If an MCP server process exits unexpectedly,
  subsequent tool calls to that server fail with a connection error.
  The agent handles this like any other tool failure.

## Protocol Support

| MCP Feature | Status |
|-------------|--------|
| `tools/list` | ✅ Supported (with cursor pagination) |
| `tools/call` | ✅ Supported |
| `initialize` / `initialized` | ✅ Supported |
| Stdio transport | ✅ Supported |
| SSE transport | ✅ Supported |
| Resources | 🚧 Not yet used (responses are accepted) |
| Prompts | 🚧 Not yet used |

## Technical Notes

- **Request/response matching**: Each JSON-RPC request gets a unique
  ID. A dedicated background goroutine (`readLoop`) reads all response
  lines from the server and dispatches them to the waiting caller by ID.
  This eliminates the risk of response desynchronization on cancellation.

- **No process killing on cancel**: If the user cancels a task during
  an MCP tool call, the tool is simply abandoned — the server process
  continues running and its response is discarded. This preserves
  server state and avoids reconnection overhead.

- **Concurrent tool execution**: Multiple MCP tools from different
  servers can execute concurrently, matching the same concurrency model
  as built-in tools.
