package llm

import (
	"encoding/json"
	"testing"
)

func TestGenerateSchema(t *testing.T) {
	type TestInput struct {
		Name        string  `json:"name" jsonschema:"required" jsonschema_desc:"The name of the item"`
		Description string  `json:"description" jsonschema_desc:"Optional description"`
		Type        string  `json:"type" jsonschema:"required" jsonschema_enum:"foo|bar|baz" jsonschema_desc:"The type"`
		Count       int     `json:"count" jsonschema_desc:"Number of items"`
		Rate        float64 `json:"rate" jsonschema_desc:"Rate per second"`
		Enabled     bool    `json:"enabled" jsonschema_desc:"Whether enabled"`
	}

	schema, err := GenerateSchema(TestInput{})
	if err != nil {
		t.Fatalf("GenerateSchema failed: %v", err)
	}

	// Verify it's valid JSON
	var result map[string]any
	if err := json.Unmarshal(schema, &result); err != nil {
		t.Fatalf("Generated invalid JSON: %v", err)
	}

	// Check type
	if result["type"] != "object" {
		t.Errorf("Expected type 'object', got %v", result["type"])
	}

	// Check required fields
	required, ok := result["required"].([]any)
	if !ok {
		t.Fatal("required field is not an array")
	}
	if len(required) != 2 {
		t.Errorf("Expected 2 required fields, got %d", len(required))
	}

	// Check properties
	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties is not an object")
	}

	// Check name field (string)
	nameField, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatal("name property is not an object")
	}
	if nameField["type"] != "string" {
		t.Errorf("Expected name type 'string', got %v", nameField["type"])
	}
	if nameField["description"] != "The name of the item" {
		t.Errorf("Unexpected name description: %v", nameField["description"])
	}

	// Check count field (int → integer)
	countField, ok := props["count"].(map[string]any)
	if !ok {
		t.Fatal("count property is not an object")
	}
	if countField["type"] != "integer" {
		t.Errorf("Expected count type 'integer', got %v", countField["type"])
	}

	// Check rate field (float64 → number)
	rateField, ok := props["rate"].(map[string]any)
	if !ok {
		t.Fatal("rate property is not an object")
	}
	if rateField["type"] != "number" {
		t.Errorf("Expected rate type 'number', got %v", rateField["type"])
	}

	// Check enabled field (bool → boolean)
	enabledField, ok := props["enabled"].(map[string]any)
	if !ok {
		t.Fatal("enabled property is not an object")
	}
	if enabledField["type"] != "boolean" {
		t.Errorf("Expected enabled type 'boolean', got %v", enabledField["type"])
	}

	// Check enum field
	typeField, ok := props["type"].(map[string]any)
	if !ok {
		t.Fatal("type property is not an object")
	}
	enum, ok := typeField["enum"].([]any)
	if !ok {
		t.Fatal("enum is not an array")
	}
	if len(enum) != 3 {
		t.Errorf("Expected 3 enum values, got %d", len(enum))
	}

	t.Logf("Generated schema: %s", string(schema))
}

func TestGenerateSchemaWithCommaInDescription(t *testing.T) {
	type Input struct {
		FileType string `json:"file_type" jsonschema_desc:"File type filter (e.g. go, python, rust)"`
		Glob     string `json:"glob" jsonschema_desc:"Glob pattern (e.g. *.go, *.{ts,tsx})"`
	}

	schema, err := GenerateSchema(Input{})
	if err != nil {
		t.Fatalf("GenerateSchema failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	props, ok := parsed["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties is not an object")
	}

	ft, ok := props["file_type"].(map[string]any)
	if !ok {
		t.Fatal("file_type property is not an object")
	}
	if ft["description"] != "File type filter (e.g. go, python, rust)" {
		t.Errorf("file_type description = %q, want %q", ft["description"], "File type filter (e.g. go, python, rust)")
	}

	g, ok := props["glob"].(map[string]any)
	if !ok {
		t.Fatal("glob property is not an object")
	}
	if g["description"] != "Glob pattern (e.g. *.go, *.{ts,tsx})" {
		t.Errorf("glob description = %q, want %q", g["description"], "Glob pattern (e.g. *.go, *.{ts,tsx})")
	}
}
