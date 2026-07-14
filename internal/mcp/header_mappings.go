package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ============================================================================
// x-mcp-header Support
// ============================================================================
//
// Functions in this file handle the x-mcp-header extension (2026-07-28+),
// which allows servers to designate tool parameters to be mirrored as HTTP
// headers (Mcp-Param-{Name}) so intermediaries can inspect requests without
// parsing the body.

// x-mcp-header schema type constants.
const (
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeBoolean = "boolean"
	headerTrue        = "true"
	headerFalse       = "false"
)

// parseHeaderMappings extracts x-mcp-header annotations from a tool's
// inputSchema. It walks the properties at the root level and returns
// a HeaderMapping for each parameter that has the x-mcp-header extension.
//
// Per the 2026-07-28 spec, x-mcp-header annotations are only valid on
// statically-reachable properties (direct children of the root object,
// reachable via a chain of `properties` keys). Nesting beyond root is
// permitted as long as every step is a `properties` key.
func parseHeaderMappings(schema json.RawMessage) []HeaderMapping {
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &root); err != nil || len(root.Properties) == 0 {
		return nil
	}

	var mappings []HeaderMapping
	for propName, propRaw := range root.Properties {
		var prop struct {
			Type       string                     `json:"type"`
			XMcpHeader string                     `json:"x-mcp-header"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(propRaw, &prop); err != nil {
			continue
		}

		// Direct annotation on this property.
		if prop.XMcpHeader != "" {
			// Only string, integer, boolean are valid.
			if prop.Type == schemaTypeString || prop.Type == schemaTypeInteger || prop.Type == schemaTypeBoolean {
				mappings = append(mappings, HeaderMapping{
					ParamPath:  []string{propName},
					HeaderName: prop.XMcpHeader,
					ParamType:  prop.Type,
				})
			}
			// Skip properties with x-mcp-header but unsupported type.
			continue
		}

		// Recurse into nested object properties.
		if prop.Type == schemaTypeObject && len(prop.Properties) > 0 {
			nested := parseNestedHeaderMappings(prop.Properties, []string{propName})
			mappings = append(mappings, nested...)
		}
	}

	return mappings
}

// parseNestedHeaderMappings recursively walks nested object properties
// looking for x-mcp-header annotations. parentPath is the chain of
// property keys leading to this level.
func parseNestedHeaderMappings(props map[string]json.RawMessage, parentPath []string) []HeaderMapping {
	var mappings []HeaderMapping
	for propName, propRaw := range props {
		var prop struct {
			Type       string                     `json:"type"`
			XMcpHeader string                     `json:"x-mcp-header"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(propRaw, &prop); err != nil {
			continue
		}

		path := append(append([]string{}, parentPath...), propName)

		if prop.XMcpHeader != "" {
			if prop.Type == schemaTypeString || prop.Type == schemaTypeInteger || prop.Type == schemaTypeBoolean {
				mappings = append(mappings, HeaderMapping{
					ParamPath:  path,
					HeaderName: prop.XMcpHeader,
					ParamType:  prop.Type,
				})
			}
			continue
		}

		if prop.Type == schemaTypeObject && len(prop.Properties) > 0 {
			nested := parseNestedHeaderMappings(prop.Properties, path)
			mappings = append(mappings, nested...)
		}
	}
	return mappings
}

// encodeHeaderValue converts a parameter value to its HTTP header string
// representation per the MCP 2026-07-28 spec.
//
// Returns the encoded value and whether Base64 encoding was applied.
func encodeHeaderValue(value any, paramType string) (string, bool) {
	var raw string
	switch paramType {
	case schemaTypeString:
		s, ok := value.(string)
		if !ok {
			return "", false
		}
		raw = s
	case schemaTypeInteger:
		// JSON numbers decode as float64 by default.
		switch v := value.(type) {
		case float64:
			raw = fmt.Sprintf("%.0f", v)
		case int:
			raw = fmt.Sprintf("%d", v)
		case int64:
			raw = fmt.Sprintf("%d", v)
		default:
			return "", false
		}
	case schemaTypeBoolean:
		b, ok := value.(bool)
		if !ok {
			return "", false
		}
		if b {
			raw = headerTrue
		} else {
			raw = headerFalse
		}
	default:
		return "", false
	}

	if needsBase64Encoding(raw) {
		return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(raw)) + "?=", true
	}
	return raw, false
}

// needsBase64Encoding returns true if the value must be Base64-encoded
// for safe use in an HTTP header per RFC 9110.
func needsBase64Encoding(s string) bool {
	if s == "" {
		return false
	}
	// Sentinel pattern: if value looks like =?base64?...?=, encode it.
	if strings.HasPrefix(s, "=?base64?") && strings.HasSuffix(s, "?=") {
		return true
	}
	// Check for leading/trailing whitespace.
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return true
	}
	// Check for non-ASCII or control characters.
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			return true
		}
	}
	return false
}

// resolveNestedValue looks up a value in a nested map by path.
// For path ["location", "region"], it returns argsMap["location"]["region"].
func resolveNestedValue(m map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	current := m
	for i, key := range path {
		val, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return val, true
		}
		next, ok := val.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}
