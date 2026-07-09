// Package auth provides OAuth 2.1 client functionality for MCP servers.
//
// Client credentials (client_id and client_secret) are provided by the
// user in their mcp.conf file. No built-in credentials are shipped —
// users register their own OAuth app with the service and configure it
// via auth-client-id and auth-client-secret fields.
package auth
