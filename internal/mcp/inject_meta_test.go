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
