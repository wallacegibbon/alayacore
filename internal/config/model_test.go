package config

import (
	"strings"
	"testing"
)

func TestFormatModelList_Empty(t *testing.T) {
	out := FormatModelList(nil)
	if out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
	out = FormatModelList([]ModelConfig{})
	if out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
}

func TestFormatModelList_Single(t *testing.T) {
	models := []ModelConfig{
		{
			Name:         "test-model",
			ProtocolType: "anthropic",
			BaseURL:      "http://localhost:11434",
			APIKey:       "nokey",
			ModelName:    "test",
			ContextLimit: 64000,
		},
	}
	out := FormatModelList(models)

	if !strings.Contains(out, `name: "test-model"`) {
		t.Errorf("missing name, got: %s", out)
	}
	if !strings.Contains(out, `protocol_type: "anthropic"`) {
		t.Errorf("missing protocol_type, got: %s", out)
	}
	if !strings.Contains(out, `base_url: "http://localhost:11434"`) {
		t.Errorf("missing base_url, got: %s", out)
	}
	if !strings.Contains(out, `api_key: "nokey"`) {
		t.Errorf("missing api_key, got: %s", out)
	}
	if !strings.Contains(out, `model_name: "test"`) {
		t.Errorf("missing model_name, got: %s", out)
	}
	if !strings.Contains(out, `context_limit: 64000`) {
		t.Errorf("missing context_limit, got: %s", out)
	}

	// No trailing blank lines
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("trailing blank line: %q", out)
	}
	// Ends with single newline
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("missing trailing newline: %q", out)
	}
	// No leading/trailing --- for single model
	if strings.HasPrefix(out, "---") {
		t.Errorf("unexpected leading ---: %q", out)
	}
	if strings.Count(out, "---") > 0 {
		t.Errorf("unexpected --- for single model: %q", out)
	}
}

func TestFormatModelList_Multiple(t *testing.T) {
	models := []ModelConfig{
		{Name: "model-a", ProtocolType: "openai", BaseURL: "http://a", APIKey: "k", ModelName: "m-a"},
		{Name: "model-b", ProtocolType: "anthropic", BaseURL: "http://b", APIKey: "k", ModelName: "m-b"},
	}
	out := FormatModelList(models)

	if !strings.Contains(out, `name: "model-a"`) {
		t.Errorf("missing model-a, got: %s", out)
	}
	if !strings.Contains(out, `name: "model-b"`) {
		t.Errorf("missing model-b, got: %s", out)
	}

	// Separated by ---
	if !strings.Contains(out, "\n---\n") {
		t.Errorf("missing --- separator, got: %s", out)
	}

	// No blank line before --- (FormatKeyValue trailing \n was trimmed)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if line == "---" && i > 0 {
			if lines[i-1] == "" {
				t.Errorf("blank line before --- at line %d", i)
			}
		}
	}
}

func TestFormatModelList_OmitsZeroValues(t *testing.T) {
	models := []ModelConfig{
		{
			Name:         "test",
			ProtocolType: "openai",
			BaseURL:      "http://localhost",
			APIKey:       "k",
			ModelName:    "m",
			// ContextLimit and MaxTokens are 0 (zero) → omitempty → not written
		},
	}
	out := FormatModelList(models)

	if strings.Contains(out, "context_limit:") {
		t.Errorf("expected context_limit to be omitted (0 value), got: %s", out)
	}
	if strings.Contains(out, "max_tokens:") {
		t.Errorf("expected max_tokens to be omitted (0 value), got: %s", out)
	}
}

func TestFormatModelList_OmitsID(t *testing.T) {
	models := []ModelConfig{
		{
			ID:           42,
			Name:         "test",
			ProtocolType: "openai",
			BaseURL:      "http://localhost",
			APIKey:       "k",
			ModelName:    "m",
		},
	}
	out := FormatModelList(models)

	// ID has config:"-" tag, should never appear in output
	if strings.Contains(out, "id:") {
		t.Errorf("expected id to be omitted (config:\"-\"), got: %s", out)
	}
}

func TestParseModelList_Empty(t *testing.T) {
	models, warns := ParseModelList("")
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warns), warns)
	}

	models, warns = ParseModelList("  \n  \n  ")
	if len(models) != 0 {
		t.Errorf("expected 0 models for whitespace, got %d", len(models))
	}
}

func TestParseModelList_Single(t *testing.T) {
	content := `name: "test-model"
protocol_type: "anthropic"
base_url: "http://localhost:11434"
api_key: "nokey"
model_name: "test"
context_limit: 64000
`
	models, warns := ParseModelList(content)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warns), warns)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	m := models[0]
	if m.Name != "test-model" {
		t.Errorf("Name = %q, want %q", m.Name, "test-model")
	}
	if m.ProtocolType != "anthropic" {
		t.Errorf("ProtocolType = %q, want %q", m.ProtocolType, "anthropic")
	}
	if m.BaseURL != "http://localhost:11434" {
		t.Errorf("BaseURL = %q, want %q", m.BaseURL, "http://localhost:11434")
	}
	if m.ModelName != "test" {
		t.Errorf("ModelName = %q, want %q", m.ModelName, "test")
	}
	if m.ContextLimit != 64000 {
		t.Errorf("ContextLimit = %d, want %d", m.ContextLimit, 64000)
	}
}

func TestParseModelList_Multiple(t *testing.T) {
	content := `name: "model-a"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "m-a"
---
name: "model-b"
protocol_type: "anthropic"
base_url: "http://b"
api_key: "k"
model_name: "m-b"
`
	models, warns := ParseModelList(content)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warns), warns)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "model-a" {
		t.Errorf("model[0].Name = %q, want %q", models[0].Name, "model-a")
	}
	if models[1].Name != "model-b" {
		t.Errorf("model[1].Name = %q, want %q", models[1].Name, "model-b")
	}
}

func TestParseModelList_SkipsEmptyBlocks(t *testing.T) {
	content := `name: "first"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "f"
---


---
name: "second"
protocol_type: "openai"
base_url: "http://b"
api_key: "k"
model_name: "s"
`
	models, warns := ParseModelList(content)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warns), warns)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
}

func TestParseModelList_SkipsCommentedBlocks(t *testing.T) {
	// Lines starting with # have no colon → silently ignored as unknown keys.
	// The block is skipped because Name and ModelName are empty.
	content := `name: "active"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "active"
---
#name: "commented-out"
#protocol_type: "openai"
#base_url: "http://b"
#api_key: "k"
#model_name: "commented"
`
	models, _ := ParseModelList(content)
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "active" {
		t.Errorf("expected 'active', got %q", models[0].Name)
	}
}

func TestParseModelList_SkipsNamelessBlocks(t *testing.T) {
	content := `name: "valid"
protocol_type: "openai"
base_url: "http://a"
api_key: "k"
model_name: "valid"
---
# This block has no name or model_name — should be skipped
protocol_type: "anthropic"
base_url: "http://b"
api_key: "k"
`
	models, _ := ParseModelList(content)
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "valid" {
		t.Errorf("expected 'valid', got %q", models[0].Name)
	}
}

func TestParseModelList_RoundTrip(t *testing.T) {
	original := []ModelConfig{
		{Name: "m1", ProtocolType: "openai", BaseURL: "http://a", APIKey: "k1", ModelName: "m1", ContextLimit: 1000},
		{Name: "m2", ProtocolType: "anthropic", BaseURL: "http://b", APIKey: "k2", ModelName: "m2", MaxTokens: 500},
	}
	formatted := FormatModelList(original)
	parsed, warns := ParseModelList(formatted)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warns), warns)
	}
	if len(parsed) != len(original) {
		t.Fatalf("expected %d models, got %d", len(original), len(parsed))
	}
	for i := range original {
		if parsed[i].Name != original[i].Name {
			t.Errorf("model[%d].Name = %q, want %q", i, parsed[i].Name, original[i].Name)
		}
		if parsed[i].ProtocolType != original[i].ProtocolType {
			t.Errorf("model[%d].ProtocolType = %q, want %q", i, parsed[i].ProtocolType, original[i].ProtocolType)
		}
		if parsed[i].BaseURL != original[i].BaseURL {
			t.Errorf("model[%d].BaseURL = %q, want %q", i, parsed[i].BaseURL, original[i].BaseURL)
		}
		if parsed[i].ModelName != original[i].ModelName {
			t.Errorf("model[%d].ModelName = %q, want %q", i, parsed[i].ModelName, original[i].ModelName)
		}
		if parsed[i].ContextLimit != original[i].ContextLimit {
			t.Errorf("model[%d].ContextLimit = %d, want %d", i, parsed[i].ContextLimit, original[i].ContextLimit)
		}
		if parsed[i].MaxTokens != original[i].MaxTokens {
			t.Errorf("model[%d].MaxTokens = %d, want %d", i, parsed[i].MaxTokens, original[i].MaxTokens)
		}
	}
}

func TestFormatThenParse_WithID(t *testing.T) {
	// ID (config:"-") should never survive a format+parse cycle.
	original := []ModelConfig{
		{ID: 99, Name: "test", ProtocolType: "openai", BaseURL: "http://a", APIKey: "k", ModelName: "m"},
	}
	formatted := FormatModelList(original)
	parsed, _ := ParseModelList(formatted)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 model, got %d", len(parsed))
	}
	if parsed[0].ID != 0 {
		t.Errorf("ID should be reset to 0 after format+parse, got %d", parsed[0].ID)
	}
}
