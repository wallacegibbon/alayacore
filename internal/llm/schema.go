package llm

import (
	"encoding/json"
	"reflect"
	"strings"
)

// SchemaField tags for struct fields
// Use like: `json:"name" jsonschema:"required,description=The name of the file"`
//
// Supported tags:
//   - required: marks the field as required
//   - description=...: sets the field description
//   - type=...: overrides the inferred type
//   - enum=...: pipe-separated list of allowed values
//
// Type inference (automatic, no tag needed):
//   - string → "string"
//   - int, int8, int16, int32, int64 → "integer"
//   - uint, uint8, uint16, uint32, uint64 → "integer"
//   - float32, float64 → "number"
//   - bool → "boolean"
type SchemaField struct {
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

const (
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeNumber  = "number"
	schemaTypeBoolean = "boolean"
)

// inferSchemaType maps Go types to JSON schema types
func inferSchemaType(t reflect.Type) string {
	// Handle pointer types
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return schemaTypeString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return schemaTypeInteger
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schemaTypeInteger
	case reflect.Float32, reflect.Float64:
		return schemaTypeNumber
	case reflect.Bool:
		return schemaTypeBoolean
	default:
		return schemaTypeString // fallback
	}
}

// GenerateSchema generates a JSON schema from a struct using reflection
func GenerateSchema(v interface{}) json.RawMessage {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic("GenerateSchema: expected struct")
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": make(map[string]SchemaField),
	}
	properties := make(map[string]SchemaField)
	schema["properties"] = properties
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous {
			continue
		}

		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		fieldName := strings.Split(jsonTag, ",")[0]
		if fieldName == "" {
			fieldName = field.Name
		}

		schemaField := SchemaField{
			Type: inferSchemaType(field.Type),
		}

		// Parse jsonschema tag
		if tag := field.Tag.Get("jsonschema"); tag != "" {
			parts := strings.Split(tag, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				switch {
				case part == "required":
					required = append(required, fieldName)
				case strings.HasPrefix(part, "description="):
					schemaField.Description = strings.TrimPrefix(part, "description=")
				case strings.HasPrefix(part, "type="):
					schemaField.Type = strings.TrimPrefix(part, "type=")
				case strings.HasPrefix(part, "enum="):
					enumStr := strings.TrimPrefix(part, "enum=")
					schemaField.Enum = strings.Split(enumStr, "|")
				}
			}
		}

		properties[fieldName] = schemaField
	}

	if len(required) > 0 {
		schema["required"] = required
	}

	result, err := json.Marshal(schema)
	if err != nil {
		panic("GenerateSchema: " + err.Error())
	}
	return json.RawMessage(result)
}
