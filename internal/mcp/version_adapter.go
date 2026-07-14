package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// ============================================================================
// Compile-time checks
// ============================================================================

var (
	_ Adapter = (*AdapterV20251125)(nil)
	_ Adapter = (*AdapterV20260728)(nil)
)

// ============================================================================
// Shared helpers
// ============================================================================

// doDiscover sends server/discover and stores the server's capabilities.
// preferredVersion is the protocol version the adapter wants to use;
// the method picks the best mutually supported version.
func (c *Client) doDiscover(ctx context.Context, preferredVersion string) (string, error) {
	result, err := c.sendRequest(ctx, methodDiscover, nil)
	if err != nil {
		return "", err
	}

	var discover DiscoverResult
	if err := json.Unmarshal(result, &discover); err != nil {
		return "", fmt.Errorf("parse server/discover result: %w", err)
	}

	c.capabilities = discover.Capabilities
	c.serverInfo = discover.ServerInfo
	c.instructions = discover.Instructions

	// Pick the best mutually supported version.
	for _, v := range discover.SupportedVersions {
		if v == preferredVersion {
			return v, nil
		}
	}
	for _, v := range discover.SupportedVersions {
		if v == protocolVersion { // 2025-11-25
			return v, nil
		}
	}
	return "", fmt.Errorf("no compatible protocol version: server supports %v",
		discover.SupportedVersions)
}

// injectMeta merges a _meta object into serialized JSON-RPC params
// using efficient raw JSON manipulation.
func injectMeta(params json.RawMessage, meta any) (json.RawMessage, error) {
	if meta == nil {
		return params, nil
	}

	metaData, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal _meta: %w", err)
	}

	if len(params) == 0 {
		return json.RawMessage(`{"_meta":` + string(metaData) + `}`), nil
	}

	end := len(params)
	for end > 0 && (params[end-1] == ' ' || params[end-1] == '\t' || params[end-1] == '\n' || params[end-1] == '\r') {
		end--
	}
	if end == 0 || params[end-1] != '}' {
		return nil, fmt.Errorf("params must be a JSON object for _meta injection, got: %s", string(params))
	}

	result := make([]byte, end-1+len(metaData)+13)
	copy(result, params[:end-1])
	result[end-1] = ','
	offset := end
	copy(result[offset:], `"_meta":`)
	offset += 8
	copy(result[offset:], metaData)
	offset += len(metaData)
	result[offset] = '}'
	offset++
	return result[:offset], nil
}
