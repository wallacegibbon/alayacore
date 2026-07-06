package llm

import (
	"encoding/json"
)

// ============================================================================
// Simplified JSON Schema Parser (no external dependencies)
// ============================================================================

// schemaTypeObject is the JSON Schema type name for objects.
const schemaTypeObject = "object"

// schemaInfo is a simplified representation of a JSON Schema,
// containing only the fields needed for tool input repair.
// It is a tree structure that mirrors the schema's nesting.
type schemaInfo struct {
	// typeName is the JSON Schema type (e.g., "object", "array", "string",
	// "integer", "number", "boolean"). Empty string means any type.
	typeName string

	// properties maps field names to their schema, for objects.
	properties map[string]*schemaInfo

	// items is the schema for array elements, for arrays.
	items *schemaInfo

	// required is the set of field names that must be present.
	// Used to distinguish null-for-optional from null-for-required.
	required map[string]bool
}

// parseSchema parses a raw JSON Schema into the simplified schemaInfo form.
// Returns nil if the input is empty, not valid JSON, or not an object schema.
func parseSchema(raw json.RawMessage) *schemaInfo {
	if len(raw) == 0 {
		return nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	return parseSchemaMap(schema)
}

// parseSchemaMap recursively converts a JSON Schema map to schemaInfo.
func parseSchemaMap(m map[string]any) *schemaInfo {
	info := &schemaInfo{}

	// Extract type. When absent, leave typeName empty (means "any type").
	if t, ok := m["type"].(string); ok {
		info.typeName = t
	}

	// Extract properties (for object type)
	if props, ok := m["properties"].(map[string]any); ok {
		info.properties = make(map[string]*schemaInfo, len(props))
		for key, val := range props {
			if propMap, ok := val.(map[string]any); ok {
				info.properties[key] = parseSchemaMap(propMap)
			}
		}
	}

	// Extract items schema (for array type)
	if items, ok := m["items"].(map[string]any); ok {
		info.items = parseSchemaMap(items)
	}

	// Extract required field names
	if req, ok := m["required"].([]any); ok {
		info.required = make(map[string]bool, len(req))
		for _, r := range req {
			if rs, ok := r.(string); ok {
				info.required[rs] = true
			}
		}
	}

	return info
}

// ============================================================================
// Tool Input Repair
// ============================================================================

// RepairToolInput repairs a tool's input JSON based on its JSON Schema.
// It handles the 4 common LLM output errors:
//
//  1. null for optional fields — removes the field entirely
//  2. JSON-stringified array ("[\"a\",\"b\"]") — parses it to a real array
//  3. Bare object where array expected — wraps in a single-element array;
//     also removes empty object placeholders ({}) in arrays when the item
//     schema has required fields
//  4. Bare string where array expected — wraps in a single-element array
//
// If the input is already valid, it is returned unchanged.
// If the schema cannot be parsed, the input is returned as-is (fail-safe).
func RepairToolInput(input json.RawMessage, schema json.RawMessage) json.RawMessage {
	schemaObj := parseSchema(schema)
	if schemaObj == nil || schemaObj.typeName == "" {
		// No type info means "any type" — nothing to validate against
		return input
	}

	var inputMap map[string]any
	if err := json.Unmarshal(input, &inputMap); err != nil {
		return input
	}

	// Root must be an object for tool input to make sense
	if schemaObj.typeName != schemaTypeObject {
		return input
	}

	result := repairObject(inputMap, schemaObj)

	fixed, err := json.Marshal(result)
	if err != nil {
		return input
	}

	// Only return fixed JSON if it actually differs from the original
	if string(fixed) == string(input) {
		return input
	}
	return fixed
}

// ============================================================================
// Recursive Repair Helpers
// ============================================================================

// repairObject repairs a JSON object according to its schema.
// Handles Pattern 1 (null removal) and recurses into nested values.
func repairObject(obj map[string]any, schema *schemaInfo) map[string]any {
	result := make(map[string]any, len(obj))

	for k, v := range obj {
		propSchema := schema.properties[k]

		// Pattern 1: null for optional fields → remove the field
		if v == nil {
			if !schema.required[k] {
				continue // drop null optional field
			}
			// Keep null for required fields (models sometimes send null
			// for required fields too; better to keep than to break the call)
			result[k] = v
			continue
		}

		if propSchema == nil {
			// No schema info for this field — keep as-is
			result[k] = v
			continue
		}

		result[k] = repairValue(v, propSchema)
	}

	return result
}

// repairValue repairs a single JSON value according to its schema type.
func repairValue(value any, schema *schemaInfo) any {
	if value == nil || schema == nil {
		return value
	}

	switch schema.typeName {
	case schemaTypeObject:
		if obj, ok := value.(map[string]any); ok {
			return repairObject(obj, schema)
		}
		return value // can't fix non-object into object

	case "array":
		return repairArrayValue(value, schema)

	default:
		// Primitive types or unknown type — nothing to repair
		return value
	}
}

// repairArrayValue handles a value that should be an array per schema.
// It handles Patterns 2, 3, and 4.
func repairArrayValue(value any, schema *schemaInfo) any {
	// Already a valid array — repair each element
	if arr, ok := value.([]any); ok {
		return repairArrayElements(arr, schema)
	}

	// Pattern 2: JSON string that looks like a serialized array
	if str, ok := value.(string); ok {
		var arr []any
		if err := json.Unmarshal([]byte(str), &arr); err == nil {
			return repairArrayElements(arr, schema)
		}
		// Pattern 4: bare string — wrap in single-element array
		return repairSingleElementArray(str, schema)
	}

	// Pattern 3: bare object/number/bool where array expected — wrap
	return repairSingleElementArray(value, schema)
}

// repairSingleElementArray wraps a single value in an array and repairs it.
func repairSingleElementArray(value any, schema *schemaInfo) []any {
	if schema.items != nil {
		return []any{repairValue(value, schema.items)}
	}
	return []any{value}
}

// repairArrayElements repairs each element in an array.
// Also handles the "empty placeholder" sub-case of Pattern 3.
func repairArrayElements(arr []any, schema *schemaInfo) []any {
	result := make([]any, 0, len(arr))
	for _, elem := range arr {
		if elem == nil {
			continue // skip null elements
		}

		// Skip empty object placeholders (Pattern 3 sub-case):
		// Models sometimes emit {} as a placeholder when they intend
		// to fill in the element later or as a no-op. We detect this
		// by checking if the item schema has required fields and the
		// object is empty — a legitimate empty object with no required
		// fields is kept.
		if obj, ok := elem.(map[string]any); ok && len(obj) == 0 {
			if schema.items != nil && len(schema.items.required) > 0 {
				continue
			}
		}

		if schema.items != nil {
			result = append(result, repairValue(elem, schema.items))
		} else {
			result = append(result, elem)
		}
	}
	return result
}
