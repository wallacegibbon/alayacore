package config

import (
	"reflect"
	"strconv"
	"strings"
	"time"
)

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
func ParseKeyValue(content string, target interface{}) {
	parseKeyValue(content, target, false)
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
			tagToField[tag] = i
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

		// Remove surrounding quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

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
