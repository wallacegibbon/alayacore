package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseModelConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []ModelConfig
	}{
		{
			name: "single model",
			input: `name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "test-key"
model_name: "gpt-4o"
context_limit: 128000`,
			expected: []ModelConfig{
				{
					Name:         "OpenAI GPT-4o",
					ProtocolType: "openai",
					BaseURL:      "https://api.openai.com/v1",
					APIKey:       "test-key",
					ModelName:    "gpt-4o",
					ContextLimit: 128000,
				},
			},
		},
		{
			name: "multiple models",
			input: `name: "OpenAI GPT-4o"
protocol_type: "openai"
base_url: "https://api.openai.com/v1"
api_key: "key1"
model_name: "gpt-4o"
---
name: "Ollama (127.0.0.1) / GPT OSS 20B"
protocol_type: "anthropic"
base_url: "http://127.0.0.1:11434"
api_key: "key2"
model_name: "gpt-oss:20b"`,
			expected: []ModelConfig{
				{
					Name:         "OpenAI GPT-4o",
					ProtocolType: "openai",
					BaseURL:      "https://api.openai.com/v1",
					APIKey:       "key1",
					ModelName:    "gpt-4o",
				},
				{
					Name:         "Ollama (127.0.0.1) / GPT OSS 20B",
					ProtocolType: "anthropic",
					BaseURL:      "http://127.0.0.1:11434",
					APIKey:       "key2",
					ModelName:    "gpt-oss:20b",
				},
			},
		},
		{
			name: "with comments and empty lines",
			input: `# This is a comment
name: "Test Model"
protocol_type: "openai"

base_url: "https://api.example.com"
api_key: "secret"
model_name: "test"`,
			expected: []ModelConfig{
				{
					Name:         "Test Model",
					ProtocolType: "openai",
					BaseURL:      "https://api.example.com",
					APIKey:       "secret",
					ModelName:    "test",
				},
			},
		},
		{
			name: "single quotes",
			input: `name: 'Test Model'
protocol_type: 'anthropic'
base_url: 'https://api.example.com'
api_key: 'secret'
model_name: 'claude'`,
			expected: []ModelConfig{
				{
					Name:         "Test Model",
					ProtocolType: "anthropic",
					BaseURL:      "https://api.example.com",
					APIKey:       "secret",
					ModelName:    "claude",
				},
			},
		},
		{
			name:     "empty input",
			input:    ``,
			expected: nil,
		},
		{
			name: "only whitespace and comments",
			input: `# Comment
  
# Another comment`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, warnings := parseModelConfig(tt.input)
			_ = warnings

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d models, got %d", len(tt.expected), len(result))
			}

			for i, model := range result {
				if model.Name != tt.expected[i].Name {
					t.Errorf("model %d: expected Name=%q, got %q", i, tt.expected[i].Name, model.Name)
				}
				if model.ProtocolType != tt.expected[i].ProtocolType {
					t.Errorf("model %d: expected ProtocolType=%q, got %q", i, tt.expected[i].ProtocolType, model.ProtocolType)
				}
				if model.BaseURL != tt.expected[i].BaseURL {
					t.Errorf("model %d: expected BaseURL=%q, got %q", i, tt.expected[i].BaseURL, model.BaseURL)
				}
				if model.APIKey != tt.expected[i].APIKey {
					t.Errorf("model %d: expected APIKey=%q, got %q", i, tt.expected[i].APIKey, model.APIKey)
				}
				if model.ModelName != tt.expected[i].ModelName {
					t.Errorf("model %d: expected ModelName=%q, got %q", i, tt.expected[i].ModelName, model.ModelName)
				}
				if model.ContextLimit != tt.expected[i].ContextLimit {
					t.Errorf("model %d: expected ContextLimit=%d, got %d", i, tt.expected[i].ContextLimit, model.ContextLimit)
				}
			}
		})
	}
}

func TestParseModelBlock(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ModelConfig
	}{
		{
			name: "complete model block",
			input: `name: "Test Model"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "secret-key"
model_name: "gpt-4"
context_limit: 64000`,
			expected: ModelConfig{
				Name:         "Test Model",
				ProtocolType: "openai",
				BaseURL:      "https://api.example.com",
				APIKey:       "secret-key",
				ModelName:    "gpt-4",
				ContextLimit: 64000,
			},
		},
		{
			name: "partial model block",
			input: `name: "Minimal Model"
protocol_type: "openai"
base_url: "https://api.example.com"
model_name: "mini"`,
			expected: ModelConfig{
				Name:         "Minimal Model",
				ProtocolType: "openai",
				BaseURL:      "https://api.example.com",
				ModelName:    "mini",
			},
		},
		{
			name:     "empty block",
			input:    "",
			expected: ModelConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models, warnings := parseModelConfig(tt.input)
			_ = warnings
			var result ModelConfig
			if len(models) > 0 {
				result = models[0]
			}

			if result.Name != tt.expected.Name {
				t.Errorf("expected Name=%q, got %q", tt.expected.Name, result.Name)
			}
			if result.ProtocolType != tt.expected.ProtocolType {
				t.Errorf("expected ProtocolType=%q, got %q", tt.expected.ProtocolType, result.ProtocolType)
			}
			if result.BaseURL != tt.expected.BaseURL {
				t.Errorf("expected BaseURL=%q, got %q", tt.expected.BaseURL, result.BaseURL)
			}
			if result.APIKey != tt.expected.APIKey {
				t.Errorf("expected APIKey=%q, got %q", tt.expected.APIKey, result.APIKey)
			}
			if result.ModelName != tt.expected.ModelName {
				t.Errorf("expected ModelName=%q, got %q", tt.expected.ModelName, result.ModelName)
			}
			if result.ContextLimit != tt.expected.ContextLimit {
				t.Errorf("expected ContextLimit=%d, got %d", tt.expected.ContextLimit, result.ContextLimit)
			}
		})
	}
}

func TestModelManagerReloadResetsIDs(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "model.conf")

	config1 := `name: "Model A"
protocol_type: "openai"
base_url: "https://api.example.com"
api_key: "key1"
model_name: "model-a"
---
name: "Model B"
protocol_type: "anthropic"
base_url: "https://api.example.com"
api_key: "key2"
model_name: "model-b"
`
	if err := os.WriteFile(configPath, []byte(config1), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	mm := NewModelManager(configPath)

	// Check initial IDs (IDs start from 1; 0 is reserved as "no model")
	models := mm.GetModels()
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != 1 {
		t.Errorf("expected first model ID to be 1, got %d", models[0].ID)
	}
	if models[1].ID != 2 {
		t.Errorf("expected second model ID to be 2, got %d", models[1].ID)
	}

	// Add a model to bump the nextID counter
	newID := mm.AddModel(ModelConfig{
		Name:         "Model C",
		ProtocolType: "openai",
		BaseURL:      "https://api.example.com",
		APIKey:       "key3",
		ModelName:    "model-c",
	})
	if newID != 3 {
		t.Errorf("expected new model ID to be 3, got %d", newID)
	}

	// Now reload from file - IDs should reset to 1, 2
	if err := mm.Reload(); err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	models = mm.GetModels()
	if len(models) != 2 {
		t.Fatalf("expected 2 models after reload, got %d", len(models))
	}
	if models[0].ID != 1 {
		t.Errorf("expected first model ID to be 1 after reload, got %d", models[0].ID)
	}
	if models[1].ID != 2 {
		t.Errorf("expected second model ID to be 2 after reload, got %d", models[1].ID)
	}
}
