package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModelConfig_InvalidProtocolType(t *testing.T) {
	input := `name: "Bad Proto"
protocol_type: "foobar"
base_url: "https://api.example.com"
api_key: "key"
model_name: "model"`

	models, warnings := parseModelConfig(input)
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown protocol_type") && strings.Contains(w, "foobar") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about unknown protocol_type, got: %v", warnings)
	}
}

func TestParseModelConfig_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantWarning  string
		dontWantWarn string
	}{
		{
			name: "missing protocol_type",
			input: `name: "No Proto"
base_url: "https://api.example.com"
model_name: "model"`,
			wantWarning: "missing required field protocol_type",
		},
		{
			name: "missing base_url",
			input: `name: "No URL"
protocol_type: "openai"
model_name: "model"`,
			wantWarning: "missing required field base_url",
		},
		{
			name: "missing model_name",
			input: `name: "No Model"
protocol_type: "openai"
base_url: "https://api.example.com"`,
			wantWarning: "missing required field model_name",
		},
		{
			name: "all required present",
			input: `name: "Complete"
protocol_type: "openai"
base_url: "https://api.example.com"
model_name: "model"`,
			dontWantWarn: "missing required field",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, warnings := parseModelConfig(tt.input)
			if tt.wantWarning != "" {
				found := false
				for _, w := range warnings {
					if strings.Contains(w, tt.wantWarning) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected warning containing %q, got: %v", tt.wantWarning, warnings)
				}
			}
			if tt.dontWantWarn != "" {
				for _, w := range warnings {
					if strings.Contains(w, tt.dontWantWarn) {
						t.Errorf("unexpected warning: %s", w)
						break
					}
				}
			}
		})
	}
}

func TestParseModelConfig_InvalidBaseURL(t *testing.T) {
	input := `name: "Bad URL"
protocol_type: "openai"
base_url: ":://bad"
api_key: "key"
model_name: "model"`

	_, warnings := parseModelConfig(input)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "invalid base_url") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about invalid base_url, got: %v", warnings)
	}
}

func TestParseModelConfig_ParseWarnings(t *testing.T) {
	input := `name: "Bad Int"
protocol_type: "openai"
base_url: "https://api.example.com"
model_name: "model"
context_limit: abc`

	_, warnings := parseModelConfig(input)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "context_limit") && strings.Contains(w, "invalid integer") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about invalid integer for context_limit, got: %v", warnings)
	}
}

func TestModelManagerGetWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")

	// Write a config with intentional issues
	badConfig := `name: "Bad Model"
protocol_type: "unknown_type"
base_url: ":://invalid"
model_name: ""
`
	if err := os.WriteFile(configPath, []byte(badConfig), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	mm := NewModelManager(configPath)
	warnings := mm.GetWarnings()

	if len(warnings) == 0 {
		t.Fatal("expected warnings from invalid config, got none")
	}

	// Check specific warnings are present
	var foundProto, foundURL, foundModelName bool
	for _, w := range warnings {
		if strings.Contains(w, "unknown protocol_type") {
			foundProto = true
		}
		if strings.Contains(w, "invalid base_url") {
			foundURL = true
		}
		if strings.Contains(w, "missing required field model_name") {
			foundModelName = true
		}
	}
	if !foundProto {
		t.Errorf("missing protocol_type warning in: %v", warnings)
	}
	if !foundURL {
		t.Errorf("missing base_url warning in: %v", warnings)
	}
	if !foundModelName {
		t.Errorf("missing model_name warning in: %v", warnings)
	}
}

func TestModelManagerNoWarningsOnValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")

	goodConfig := `name: "Good Model"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key"
model_name: "gpt-4"
`
	if err := os.WriteFile(configPath, []byte(goodConfig), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	mm := NewModelManager(configPath)
	warnings := mm.GetWarnings()

	if len(warnings) != 0 {
		t.Errorf("expected no warnings from valid config, got: %v", warnings)
	}
	if mm.ModelCount() != 1 {
		t.Errorf("expected 1 model, got %d", mm.ModelCount())
	}
}
