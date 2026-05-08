package agent

// config_format.go — shared key=value / frontmatter formatting helpers.
// Used by both RuntimeManager (runtime.conf) and Session (session frontmatter).
//
// The actual escaping logic lives in config.EscapeQuoted so there is a single
// source of truth shared with config.FormatKeyValue.

import "github.com/alayacore/alayacore/internal/config"

// escapeQuoted escapes special characters in a string that will be written
// inside double quotes in a config file. Delegates to config.EscapeQuoted.
func escapeQuoted(s string) string {
	return config.EscapeQuoted(s)
}
