package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// FormatKeyValue serializes a struct to key-value config format using `config` tags.
// The output format matches what ParseKeyValue expects:
//
//	key: value
//	key: "quoted value"
//
// Supported types: string, int*, uint*, bool, float*, time.Time.
//   - Strings are double-quoted and escaped (via escapeQuoted).
//   - time.Time is formatted as RFC3339.
//   - All other types use their default fmt/strconv formatting.
//
// Fields with a `omitempty` config tag option are skipped when zero-valued.
// Tag format: `config:"field_name"` or `config:"field_name,omitempty"`
func FormatKeyValue(v any) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return ""
	}

	t := rv.Type()
	var sb strings.Builder

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("config")
		if tag == "" {
			continue
		}

		key, opts, _ := strings.Cut(tag, ",")
		omitempty := opts == "omitempty"

		fv := rv.Field(i)
		if omitempty && fv.IsZero() {
			continue
		}

		sb.WriteString(key)
		sb.WriteString(": ")
		sb.WriteString(formatFieldValue(fv))
		sb.WriteString("\n")
	}

	return sb.String()
}

// formatFieldValue formats a single reflected value for key-value output.
func formatFieldValue(v reflect.Value) string {
	// time.Time
	if v.Type() == reflect.TypeOf(time.Time{}) {
		t := v.Interface().(time.Time) //nolint:errcheck // guarded by type check above
		return t.Format(time.RFC3339)
	}

	switch v.Kind() {
	case reflect.String:
		return escapeQuotedStr(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

// EscapeQuoted escapes special characters in a string for safe inclusion
// in a double-quoted config value. It escapes backslashes, double quotes,
// newlines, and carriage returns.
func EscapeQuoted(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

// escapeQuotedStr escapes a string and wraps it in double quotes.
func escapeQuotedStr(s string) string {
	return `"` + EscapeQuoted(s) + `"`
}
