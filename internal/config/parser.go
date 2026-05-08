package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ParseWarning represents a non-fatal issue encountered during config parsing,
// such as a value that could not be converted to the target field type.
type ParseWarning struct {
	Key   string // config key
	Value string // raw value string
	Err   string // description of the problem
}

func (w ParseWarning) String() string {
	return fmt.Sprintf("key %q: cannot parse value %q: %s", w.Key, w.Value, w.Err)
}

// ParseKeyValue parses key-value config content into a struct using `config` tags.
// The content format is:
//
//	key: value
//	key: "quoted value"
//	key: 'quoted value'
//
// Lines starting with # are comments. Empty lines are ignored.
// Multiple configs can be separated by "---" on its own line.
//
// Unknown keys are silently ignored.  Use ParseKeyValueStrict to detect them.
// Parse errors (e.g. non-numeric value for an int field) are also silently ignored.
// Use ParseKeyValueWithWarnings to collect them.
func ParseKeyValue(content string, target interface{}) {
	parseKeyValue(content, target, false)
}

// ParseKeyValueWithWarnings is like ParseKeyValue but also returns warnings for
// values that could not be converted to the target field type. This helps surface
// typos like context_limit: abc (which would otherwise silently default to 0).
func ParseKeyValueWithWarnings(content string, target interface{}) []ParseWarning {
	return parseKeyValueWithWarnings(content, target, false)
}

// ParseKeyValueStrict is like ParseKeyValue but returns any keys in content
// that did not match a struct field tag.  Callers can log or error on these.
func ParseKeyValueStrict(content string, target interface{}) []string {
	return parseKeyValueStrict(content, target, false)
}

// ParseKeyValueBlocks parses multiple config blocks separated by "---"
func ParseKeyValueBlocks(content string) []string {
	// Split by "\n---\n" to get individual blocks
	return strings.Split(content, "\n---\n")
}

// parseKeyValue is the internal implementation
//
//nolint:gocyclo // Multiple validation branches required for config parsing
func parseKeyValue(content string, target interface{}, skipHyphens bool) {
	parseKeyValueStrict(content, target, skipHyphens)
}

// parseKeyValueStrict is like parseKeyValue but returns unknown keys.
//
//nolint:gocyclo // Multiple validation branches required for config parsing
func parseKeyValueStrict(content string, target interface{}, skipHyphens bool) []string {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	v = v.Elem()
	t := v.Type()

	// Build map from config tag names to field indices
	tagToField := make(map[string]int)
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("config")
		if tag != "" {
			key, _, _ := strings.Cut(tag, ",")
			tagToField[key] = i
		}
	}

	var unknownKeys []string

	// Parse lines
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip "---" separator lines
		if skipHyphens && line == "---" {
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		value = unquoteValue(value)

		// Look up field by tag
		fieldIdx, ok := tagToField[key]
		if !ok {
			unknownKeys = append(unknownKeys, key)
			continue
		}

		field := v.Field(fieldIdx)
		setFieldValue(field, value)
	}

	return unknownKeys
}

// parseKeyValueWithWarnings is like parseKeyValueStrict but also returns
// warnings for values that could not be converted to the target type.
//
//nolint:gocyclo // Multiple validation branches required for config parsing
func parseKeyValueWithWarnings(content string, target interface{}, skipHyphens bool) []ParseWarning {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil
	}
	v = v.Elem()
	t := v.Type()

	// Build map from config tag names to field indices
	tagToField := make(map[string]int)
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("config")
		if tag != "" {
			key, _, _ := strings.Cut(tag, ",")
			tagToField[key] = i
		}
	}

	var warnings []ParseWarning

	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if skipHyphens && line == "---" {
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		value = unquoteValue(value)

		fieldIdx, ok := tagToField[key]
		if !ok {
			continue
		}

		field := v.Field(fieldIdx)
		if w := setFieldValueWithWarning(field, value, key); w != nil {
			warnings = append(warnings, *w)
		}
	}

	return warnings
}

// setFieldValueWithWarning is like setFieldValue but returns a ParseWarning
// when the value cannot be converted to the target type.
// Empty values are treated as "unset" and never produce warnings.
func setFieldValueWithWarning(field reflect.Value, value string, key string) *ParseWarning {
	// Empty values for numeric fields are not warnings — they simply mean
	// the field was not set, and the zero value is correct.
	if value == "" {
		return nil
	}

	if field.Type() == reflect.TypeOf(time.Time{}) {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return &ParseWarning{Key: key, Value: value, Err: "expected RFC3339 timestamp"}
		}
		setFieldValue(field, value)
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return warnIntField(field, value, key)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return warnUintField(field, value, key)
	case reflect.Bool:
		return warnBoolField(field, value, key)
	case reflect.Float32, reflect.Float64:
		return warnFloatField(field, value, key)
	case reflect.Slice:
		setSliceField(field, value)
		return nil
	}

	return nil
}

//nolint:gocyclo // Each numeric type is a separate case, no meaningful reduction
func warnIntField(field reflect.Value, value string, key string) *ParseWarning {
	if field.Type() == reflect.TypeOf(time.Duration(0)) {
		if _, err := time.ParseDuration(value); err != nil {
			return &ParseWarning{Key: key, Value: value, Err: "invalid duration"}
		}
		setFieldValue(field, value)
		return nil
	}
	if _, err := strconv.ParseInt(value, 10, 64); err != nil {
		return &ParseWarning{Key: key, Value: value, Err: "invalid integer"}
	}
	setFieldValue(field, value)
	return nil
}

func warnUintField(field reflect.Value, value string, key string) *ParseWarning {
	if _, err := strconv.ParseUint(value, 10, 64); err != nil {
		return &ParseWarning{Key: key, Value: value, Err: "invalid unsigned integer"}
	}
	setFieldValue(field, value)
	return nil
}

func warnBoolField(field reflect.Value, value string, key string) *ParseWarning {
	setBoolField(field, value)
	lv := strings.ToLower(value)
	switch lv {
	case "true", "1", "yes", "on", "false", "0", "no", "off", "":
		return nil
	default:
		return &ParseWarning{Key: key, Value: value, Err: "invalid boolean (expected true/false/yes/no/on/off/1/0)"}
	}
}

func warnFloatField(field reflect.Value, value string, key string) *ParseWarning {
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return &ParseWarning{Key: key, Value: value, Err: "invalid float"}
	}
	setFieldValue(field, value)
	return nil
}

// unquoteValue strips surrounding quotes from a config value.
// Double-quoted strings support escape sequences (\n, \r, \", \\).
// Single-quoted strings are treated as raw literals (no escaping).
func unquoteValue(value string) string {
	if len(value) < 2 {
		return value
	}
	if value[0] == '"' && value[len(value)-1] == '"' {
		return unescapeQuoted(value[1 : len(value)-1])
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}

// unescapeQuoted processes escape sequences in a double-quoted config value.
// Recognized sequences: \\, \", \n, \r.
// Unknown sequences (e.g. \U) are kept as-is for backward compatibility.
func unescapeQuoted(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			default:
				// Unknown escape: keep both characters as-is
				b.WriteByte('\\')
				b.WriteByte(s[i+1])
			}
			i += 2
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// setFieldValue sets a struct field value from a string
//
//nolint:gocyclo // Complex type switch required for reflection-based field setting
func setFieldValue(field reflect.Value, value string) {
	// Handle time.Time specially
	if field.Type() == reflect.TypeOf(time.Time{}) {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			field.Set(reflect.ValueOf(t))
		}
		return
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		setIntField(field, value)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u, err := strconv.ParseUint(value, 10, 64); err == nil {
			field.SetUint(u)
		}

	case reflect.Bool:
		setBoolField(field, value)

	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			field.SetFloat(f)
		}

	case reflect.Slice:
		setSliceField(field, value)
	}
}

func setIntField(field reflect.Value, value string) {
	// Handle time.Duration specially
	if field.Type() == reflect.TypeOf(time.Duration(0)) {
		if d, err := time.ParseDuration(value); err == nil {
			field.SetInt(int64(d))
		}
		return
	}
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		field.SetInt(i)
	}
}

func setBoolField(field reflect.Value, value string) {
	switch strings.ToLower(value) {
	case "true", "1", "yes", "on":
		field.SetBool(true)
	case "false", "0", "no", "off", "":
		field.SetBool(false)
	}
}

func setSliceField(field reflect.Value, value string) {
	// Handle []string with comma-separated values
	if field.Type().Elem().Kind() == reflect.String {
		parts := strings.Split(value, ",")
		slice := reflect.MakeSlice(field.Type(), len(parts), len(parts))
		for i, part := range parts {
			slice.Index(i).SetString(strings.TrimSpace(part))
		}
		field.Set(slice)
	}
}
