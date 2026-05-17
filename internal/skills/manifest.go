package skills

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseSkillMarkdown parses a SKILL.md file and extracts metadata and body
func ParseSkillMarkdown(content string) (Metadata, string, error) {
	// Check for YAML frontmatter delimiters
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		// No frontmatter - return empty metadata and full content as body
		return Metadata{}, content, nil
	}

	// Find the closing delimiter
	startIdx := -1
	endIdx := -1
	lineNum := 0

	for line := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(line) == "---" {
			if startIdx == -1 {
				startIdx = lineNum
			} else {
				endIdx = lineNum
				break
			}
		}
		lineNum++
	}

	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return Metadata{}, content, fmt.Errorf("invalid frontmatter: missing delimiters")
	}

	// Extract frontmatter YAML
	lines := strings.Split(content, "\n")
	frontmatter := strings.Join(lines[startIdx+1:endIdx], "\n")

	// Parse YAML
	var metadata Metadata
	if err := yaml.Unmarshal([]byte(frontmatter), &metadata); err != nil {
		return Metadata{}, content, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	// Validate required fields
	if metadata.Name == "" {
		return Metadata{}, content, fmt.Errorf("name is required")
	}
	if err := validateName(metadata.Name); err != nil {
		return Metadata{}, content, fmt.Errorf("invalid name: %w", err)
	}

	if metadata.Description == "" {
		return Metadata{}, content, fmt.Errorf("description is required")
	}
	if err := validateDescription(metadata.Description); err != nil {
		return Metadata{}, content, fmt.Errorf("invalid description: %w", err)
	}

	// Extract body (content after frontmatter)
	body := strings.Join(lines[endIdx+1:], "\n")
	body = strings.TrimPrefix(body, "\n")

	return metadata, body, nil
}

// validateName validates the skill name according to spec
func validateName(name string) error {
	if len(name) < 1 || len(name) > 64 {
		return fmt.Errorf("name must be 1-64 characters")
	}

	// Must be lowercase letters, numbers, and hyphens only
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return fmt.Errorf("name must contain only lowercase letters, numbers, and hyphens")
		}
	}

	// Must not start or end with hyphen
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("name must not start or end with hyphen")
	}

	// Must not contain consecutive hyphens
	if strings.Contains(name, "--") {
		return fmt.Errorf("name must not contain consecutive hyphens")
	}

	return nil
}

// validateDescription validates the skill description according to spec
func validateDescription(desc string) error {
	if len(desc) < 1 || len(desc) > 1024 {
		return fmt.Errorf("description must be 1-1024 characters")
	}
	return nil
}
