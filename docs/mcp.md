# MCP (Model Context Protocol)

AlayaCore can connect to external **MCP servers** to extend the agent's
toolset. MCP (Model Context Protocol) is an open standard that defines
how LLMs discover and invoke tools from external services — databases,
APIs, web scraping, code analysis, and more.

When MCP servers are configured, their tools and resources are automatically
discovered and merged with AlayaCore's built-in tools (`read_file`,
`write_file`, `execute_command`, etc.). The agent can use all of them
transparently in the same tool-calling loop.

## Quick Start

```bash
# Connect to an MCP server via stdio
alayacore --mcp-server "db=exec:npx @anthropic/mcp-db-server"

# Multiple servers
alayacore --mcp-server "db=exec:npx @anthropic/mcp-db-server" \
          --mcp-server "git=exec:uvx mcp-git"

# Connect to a remote MCP server via Streamable HTTP
alayacore --mcp-server "remote=http:https://example.com/mcp"

# Connect via legacy SSE
alayacore --mcp-server "remote=sse:https://example.com/sse"
```

## CLI Flags

| Flag | Description |
|------|-------------|
| `--mcp-server` | MCP server config. Can be specified multiple times. |
| `--debug-mcp` | Log raw JSON-RPC messages to `alayacore-debug-mcp-N.log` |

## `--mcp-server` Format

Single unified format: `name=transport:value`

### exec — Stdio Transport

```
name=exec:command arg1 arg2 ...
```

The server is spawned as a subprocess; JSON-RPC messages are sent/received
over stdin/stdout as newline-delimited JSON (NDJSON).

`KEY=VALUE` tokens before the command are treated as environment variables
and passed to the subprocess (in addition to the current process environment):

```bash
--mcp-server "db=exec:DB_HOST=localhost DB_PORT=5432 npx @anthropic/mcp-db-server"
--mcp-server "git=exec:GIT_DIR=/repo uvx mcp-git"
--mcp-server "search=exec:python /path/to/server.py --port 8080"
```

### sse — Legacy HTTP+SSE Transport (2024-11-05)

```
name=sse:url
```

Connects to an MCP server over Server-Sent Events. The client connects via
HTTP GET to the SSE endpoint, receives the POST endpoint URL from the
server's `endpoint` event, and sends JSON-RPC requests as HTTP POST with
responses arriving as SSE `message` events.

```bash
--mcp-server "remote=sse:https://example.com/sse"
```

### http — Streamable HTTP Transport (2025-03-26)

```
name=http:url
```

Connects to an MCP server using the new Streamable HTTP transport (spec
2025-11-25). The server provides a single HTTP endpoint for both POST
and GET. Responses can be immediate JSON or SSE streams.

```bash
--mcp-server "remote=http:https://example.com/mcp"
```

## Tool Naming

MCP tools are prefixed with the server name to avoid conflicts with
built-in tools and between servers:

```
<server>_<tool>
```

For example, with `--mcp-server "db=exec:npx @anthropic/mcp-db-server"`:

| Original tool name | Prefixed name | Description |
|--------------------|---------------|-------------|
| `query` | `db_query` | Run SQL query |
| `list_tables` | `db_list_tables` | List database tables |

The prefixed name is set as the tool's `name` in the API `tools` parameter,
so the LLM always uses it when making tool calls.

## Injected Tools

In addition to the tools exposed by the server's `tools/list`, AlayaCore
injects the following utility tools for each server that supports the
corresponding capability:

| Tool | Capability | Parameters | Description |
|------|-----------|------------|-------------|
| `{server}_read_resource` | Resources | `uri` (required) | Read a resource by URI |
| `{server}_get_prompt` | Prompts | `name` (required), `arguments` (optional) | Get a prompt template with arguments |

These tools allow the LLM to access server resources and prompts directly
within the conversation.

## How It Works

```
Agent Loop
  │
  ├── Built-in tools (read_file, write_file, ...)
  │
  └── MCP tools (db_query, git_status, db_read_resource, db_get_prompt, ...)
        │
        ▼
    Manager
      ├── Client "db"  ─── StdioTransport ─── npx @anthropic/mcp-db-server
      ├── Client "git" ─── StdioTransport ─── uvx mcp-git
      ├── Client "api" ─── SSETransport  ─── https://api.example.com/sse
      └── Client "mcp" ─── StreamableHTTP  ─── https://example.com/mcp
```

1. **Startup**: AlayaCore creates the appropriate transport for each
   server (stdio, SSE, or Streamable HTTP), performs the MCP initialize
   handshake, and calls `tools/list` to discover available tools.

2. **Tool registration**: Each discovered tool is wrapped as an
   `llm.Tool` with the prefixed name and wired to route calls back
   to the originating server. For servers that support Resources or
   Prompts, `read_resource` and `get_prompt` tools are injected.

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

6. **Stale detection**: If a server notifies that its tool list has
   changed (`notifications/tools/list_changed`), the client is marked
   stale and subsequent tool calls return a message asking the user
   to restart AlayaCore.

## Debugging

Use `--debug-mcp` to log all JSON-RPC messages:

```bash
alayacore --debug-mcp --mcp-server "db=exec:npx @anthropic/mcp-db-server"
```

This creates `alayacore-debug-mcp-N.log` (N = 0, 1, 2, ...) in the current
directory. Each run creates a new file so historical logs are preserved:

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

- **Stale server**: If the server's tool list changes at runtime
  (`notifications/tools/list_changed`), the server is marked stale
  and tool calls return: `mcp client "name": server tool list changed,
  restart required`.

## Protocol Support

| MCP Feature | Status |
|-------------|--------|
| `initialize` / `initialized` | ✅ Supported |
| `tools/list` (with cursor pagination) | ✅ Supported |
| `tools/call` | ✅ Supported |
| `resources/list` / `resources/read` | ✅ Supported (via `read_resource` tool) |
| `prompts/list` / `prompts/get` | ✅ Supported (via `get_prompt` tool) |
| `ping` | ✅ Supported |
| `notifications/cancelled` | ✅ Supported |
| `notifications/tools/list_changed` | ✅ Supported (marks server stale) |
| Stdio transport | ✅ Supported |
| Streamable HTTP transport (2025-11-25) | ✅ Supported |
| SSE transport (2024-11-05, legacy) | ✅ Supported |

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
