package mcp

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// ============================================================================
// Parse Header Mappings Tests
// ============================================================================

func TestParseHeaderMappings_Empty(t *testing.T) {
	m := parseHeaderMappings(json.RawMessage(`{"type":"object"}`))
	if len(m) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(m))
	}

	m = parseHeaderMappings(json.RawMessage(`{"type":"object","properties":{}}`))
	if len(m) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(m))
	}
}

func TestParseHeaderMappings_Basic(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"region":{"type":"string","x-mcp-header":"Region"},
			"query":{"type":"string"}
		}
	}`)
	m := parseHeaderMappings(schema)
	if len(m) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(m))
	}
	if m[0].HeaderName != "Region" {
		t.Errorf("HeaderName = %q, want Region", m[0].HeaderName)
	}
	if len(m[0].ParamPath) != 1 || m[0].ParamPath[0] != "region" {
		t.Errorf("ParamPath = %v, want [region]", m[0].ParamPath)
	}
	if m[0].ParamType != "string" {
		t.Errorf("ParamType = %q, want string", m[0].ParamType)
	}
}

func TestParseHeaderMappings_Multiple(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"region":{"type":"string","x-mcp-header":"Region"},
			"count":{"type":"integer","x-mcp-header":"Count"},
			"verbose":{"type":"boolean","x-mcp-header":"Verbose"}
		}
	}`)
	m := parseHeaderMappings(schema)
	if len(m) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(m))
	}

	names := make(map[string]string)
	for _, h := range m {
		names[h.HeaderName] = h.ParamType
	}
	if names["Region"] != "string" {
		t.Errorf("Region type = %q, want string", names["Region"])
	}
	if names["Count"] != "integer" {
		t.Errorf("Count type = %q, want integer", names["Count"])
	}
	if names["Verbose"] != "boolean" {
		t.Errorf("Verbose type = %q, want boolean", names["Verbose"])
	}
}

func TestParseHeaderMappings_SkipUnsupportedType(t *testing.T) {
	// number, array types with x-mcp-header should be skipped.
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"price":{"type":"number","x-mcp-header":"Price"},
			"tags":{"type":"array","x-mcp-header":"Tags"},
			"good":{"type":"string","x-mcp-header":"Good"}
		}
	}`)
	m := parseHeaderMappings(schema)
	if len(m) != 1 {
		t.Fatalf("expected 1 mapping (only 'good'), got %d", len(m))
	}
	if m[0].HeaderName != "Good" {
		t.Errorf("HeaderName = %q, want Good", m[0].HeaderName)
	}
}

func TestParseHeaderMappings_Nested(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"location":{
				"type":"object",
				"properties":{
					"region":{"type":"string","x-mcp-header":"Region"},
					"country":{"type":"string"}
				}
			}
		}
	}`)
	m := parseHeaderMappings(schema)
	if len(m) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(m))
	}
	if m[0].HeaderName != "Region" {
		t.Errorf("HeaderName = %q, want Region", m[0].HeaderName)
	}
	if len(m[0].ParamPath) != 2 || m[0].ParamPath[0] != "location" || m[0].ParamPath[1] != "region" {
		t.Errorf("ParamPath = %v, want [location region]", m[0].ParamPath)
	}
}

func TestParseHeaderMappings_NestedSkip(t *testing.T) {
	// The spec says x-mcp-header must be on statically-reachable properties
	// via a chain of `properties` keys. We don't validate this deeply — we
	// just walk properties recursively. Test that we don't crash on edge cases.
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"dynamic":{
				"oneOf":[
					{"type":"object","properties":{"x":{"type":"string","x-mcp-header":"X"}}}
				]
			}
		}
	}`)
	m := parseHeaderMappings(schema)
	if len(m) != 0 {
		t.Errorf("expected 0 mappings (inside oneOf), got %d", len(m))
	}
}

// ============================================================================
// Encode Header Value Tests
// ============================================================================

func TestEncodeHeaderValue_String(t *testing.T) {
	s, encoded := encodeHeaderValue("us-west1", "string")
	if encoded || s != "us-west1" {
		t.Errorf("got %q (base64=%v), want us-west1", s, encoded)
	}
}

func TestEncodeHeaderValue_Integer(t *testing.T) {
	s, encoded := encodeHeaderValue(float64(42), "integer")
	if encoded || s != "42" {
		t.Errorf("got %q (base64=%v), want 42", s, encoded)
	}
}

func TestEncodeHeaderValue_Boolean(t *testing.T) {
	s, encoded := encodeHeaderValue(true, "boolean")
	if encoded || s != "true" {
		t.Errorf("got %q (base64=%v), want true", s, encoded)
	}

	s, encoded = encodeHeaderValue(false, "boolean")
	if encoded || s != "false" {
		t.Errorf("got %q (base64=%v), want false", s, encoded)
	}
}

func TestEncodeHeaderValue_NonASCII(t *testing.T) {
	s, isBase64 := encodeHeaderValue("Hello, 世界", "string")
	if !isBase64 {
		t.Fatal("expected base64 encoding for non-ASCII")
	}
	expected := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("Hello, 世界")) + "?="
	if s != expected {
		t.Errorf("got %q, want %q", s, expected)
	}
}

func TestEncodeHeaderValue_LeadingTrailingSpace(t *testing.T) {
	s, isBase64 := encodeHeaderValue(" padded ", "string")
	if !isBase64 {
		t.Fatal("expected base64 encoding for leading/trailing space")
	}
	if s != "=?base64?"+base64.StdEncoding.EncodeToString([]byte(" padded "))+"?=" {
		t.Errorf("got %q, want base64 encoded", s)
	}
}

func TestEncodeHeaderValue_SentinelPattern(t *testing.T) {
	// Value that looks like a base64 sentinel should itself be base64 encoded.
	_, isBase64 := encodeHeaderValue("=?base64?literal?=", "string")
	if !isBase64 {
		t.Fatal("expected base64 encoding for sentinel pattern")
	}
}

func TestEncodeHeaderValue_ControlChar(t *testing.T) {
	_, isBase64 := encodeHeaderValue("line1\nline2", "string")
	if !isBase64 {
		t.Fatal("expected base64 encoding for newline")
	}
}

// ============================================================================
// Build Tool Headers Tests
// ============================================================================

func TestBuildToolHeaders_NoMappings(t *testing.T) {
	h := buildToolHeadersFromMappings(nil, json.RawMessage(`{"x":"1"}`))
	if h != nil {
		t.Errorf("expected nil, got %v", h)
	}
}

func TestBuildToolHeaders_EmptyMappings(t *testing.T) {
	h := buildToolHeadersFromMappings([]HeaderMapping{}, json.RawMessage(`{"x":"1"}`))
	if h != nil {
		t.Errorf("expected nil, got %v", h)
	}
}

func TestBuildToolHeaders_WithMappings(t *testing.T) {
	mappings := []HeaderMapping{
		{ParamPath: []string{"region"}, HeaderName: "Region", ParamType: "string"},
	}
	h := buildToolHeadersFromMappings(mappings, json.RawMessage(`{"region":"us-west1","query":"SELECT 1"}`))
	if len(h) != 1 || h["Mcp-Param-Region"] != "us-west1" {
		t.Errorf("got %v, want Mcp-Param-Region=us-west1", h)
	}
}

func TestBuildToolHeaders_MissingParam(t *testing.T) {
	mappings := []HeaderMapping{
		{ParamPath: []string{"region"}, HeaderName: "Region", ParamType: "string"},
	}
	h := buildToolHeadersFromMappings(mappings, json.RawMessage(`{"query":"SELECT 1"}`))
	if h != nil {
		t.Errorf("expected nil (no region in args), got %v", h)
	}
}

func TestBuildToolHeaders_NullParam(t *testing.T) {
	mappings := []HeaderMapping{
		{ParamPath: []string{"region"}, HeaderName: "Region", ParamType: "string"},
	}
	h := buildToolHeadersFromMappings(mappings, json.RawMessage(`{"region":null,"query":"SELECT 1"}`))
	if h != nil {
		t.Errorf("expected nil (null region), got %v", h)
	}
}

func TestBuildToolHeaders_MultipleMappings(t *testing.T) {
	mappings := []HeaderMapping{
		{ParamPath: []string{"region"}, HeaderName: "Region", ParamType: "string"},
		{ParamPath: []string{"count"}, HeaderName: "Count", ParamType: "integer"},
		{ParamPath: []string{"verbose"}, HeaderName: "Verbose", ParamType: "boolean"},
	}
	h := buildToolHeadersFromMappings(mappings, json.RawMessage(`{"region":"us-east","count":10,"verbose":true}`))
	if len(h) != 3 {
		t.Fatalf("expected 3 headers, got %d: %v", len(h), h)
	}
	if h["Mcp-Param-Region"] != "us-east" {
		t.Errorf("Region = %q, want us-east", h["Mcp-Param-Region"])
	}
	if h["Mcp-Param-Count"] != "10" {
		t.Errorf("Count = %q, want 10", h["Mcp-Param-Count"])
	}
	if h["Mcp-Param-Verbose"] != "true" {
		t.Errorf("Verbose = %q, want true", h["Mcp-Param-Verbose"])
	}
}

func TestBuildToolHeaders_NestedPath(t *testing.T) {
	mappings := []HeaderMapping{
		{ParamPath: []string{"location", "region"}, HeaderName: "Region", ParamType: "string"},
	}
	h := buildToolHeadersFromMappings(mappings, json.RawMessage(`{"location":{"region":"eu-west"},"query":"SELECT 1"}`))
	if len(h) != 1 || h["Mcp-Param-Region"] != "eu-west" {
		t.Errorf("got %v, want Mcp-Param-Region=eu-west", h)
	}
}
