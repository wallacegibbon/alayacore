package agent

// config_format.go — shared key=value / frontmatter formatting helpers.
// Used by both RuntimeManager (runtime.conf) and Session (session frontmatter).

import "strings"

// escapeQuoted escapes special characters in a string that will be written
// inside double quotes in a config file. Without this, values containing
// quotes or newlines would produce malformed output that cannot be parsed back.
func escapeQuoted(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}
