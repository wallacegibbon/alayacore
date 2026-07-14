package mcp

import (
	"encoding/json"
	"testing"
)

func TestInjectMeta_NilMeta(t *testing.T) {
	params := json.RawMessage(`{"name":"test"}`)
	result, err := injectMeta(params, nil)
	if err != nil {
		t.Fatalf("injectMeta with nil meta: %v", err)
	}
	if string(result) != string(params) {
		t.Errorf("expected %q, got %q", string(params), string(result))
	}
}

func TestInjectMeta_NilParams(t *testing.T) {
	meta := map[string]string{"version": "1.0"}
	result, err := injectMeta(nil, meta)
	if err != nil {
		t.Fatalf("injectMeta with nil params: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := parsed["_meta"]; !ok {
		t.Error("expected _meta key in result")
	}
}

func TestInjectMeta_EmptyParams(t *testing.T) {
	meta := map[string]string{"version": "1.0"}
	result, err := injectMeta(json.RawMessage{}, meta)
	if err != nil {
		t.Fatalf("injectMeta with empty params: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := parsed["_meta"]; !ok {
		t.Error("expected _meta key in result")
	}
}

func TestInjectMeta_WithExistingParams(t *testing.T) {
	params := json.RawMessage(`{"name":"test","count":42}`)
	meta := map[string]string{"version": "1.0"}
	result, err := injectMeta(params, meta)
	if err != nil {
		t.Fatalf("injectMeta: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["name"] != "test" {
		t.Errorf("expected name=test, got %v", parsed["name"])
	}
	if _, ok := parsed["_meta"]; !ok {
		t.Error("expected _meta key in result")
	}
}

func TestInjectMeta_PreservesOriginalKeys(t *testing.T) {
	params := json.RawMessage(`{"uri":"file:///data","args":{"key":"val"}}`)
	meta := map[string]string{"v": "2"}
	result, err := injectMeta(params, meta)
	if err != nil {
		t.Fatalf("injectMeta: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["uri"] != "file:///data" {
		t.Errorf("uri changed: %v", parsed["uri"])
	}
	args, ok := parsed["args"].(map[string]any)
	if !ok || args["key"] != "val" {
		t.Errorf("args corrupted: %v", parsed["args"])
	}
}

func TestInjectMeta_InvalidParams(t *testing.T) {
	// Array params — not a JSON object. Should return an error since
	// MCP params must always be JSON objects.
	params := json.RawMessage(`["a","b"]`)
	meta := map[string]string{"v": "2"}
	_, err := injectMeta(params, meta)
	if err == nil {
		t.Error("expected error for array params")
	}
}

func TestInjectMeta_OverwritesExistingMeta(t *testing.T) {
	// Params already has a _meta key — injectMeta should overwrite it,
	// not produce duplicate keys.
	params := json.RawMessage(`{"name":"tool1","_meta":{"existing":"value"}}`)
	meta := map[string]string{"version": "2.0"}
	result, err := injectMeta(params, meta)
	if err != nil {
		t.Fatalf("injectMeta with existing _meta: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify original key preserved.
	if parsed["name"] != "tool1" {
		t.Errorf("name = %v, want tool1", parsed["name"])
	}

	// Verify _meta was overwritten, not appended.
	metaVal, ok := parsed["_meta"]
	if !ok {
		t.Fatal("expected _meta key in result")
	}
	metaMap, ok := metaVal.(map[string]any)
	if !ok {
		t.Fatalf("_meta is %T, want map", metaVal)
	}
	if metaMap["version"] != "2.0" {
		t.Errorf("_meta.version = %v, want 2.0", metaMap["version"])
	}
	if _, exists := metaMap["existing"]; exists {
		t.Error("old _meta.existing should be overwritten but still present")
	}

	// Verify there's only one _meta key by counting occurrences in raw JSON.
	count := 0
	for i := 0; i < len(result)-7; i++ {
		if string(result[i:i+7]) == `"_meta"` {
			count++
		}
	}
	if count != 1 {
		t.Errorf("found %d occurrences of \"_meta\" in output (expected 1), raw: %s", count, string(result))
	}
}
