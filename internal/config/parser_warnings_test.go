package config

import (
	"testing"
	"time"
)

func TestParseKeyValueWithWarnings_IntField(t *testing.T) {
	type C struct {
		Port int `config:"port"`
	}
	tests := []struct {
		name        string
		input       string
		wantPort    int
		wantWarning bool
	}{
		{"valid int", "port: 8080", 8080, false},
		{"invalid int", "port: abc", 0, true},
		{"empty value", "port: ", 0, false}, // empty is not a parse error for int, it just fails silently
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			warnings := ParseKeyValueWithWarnings(tt.input, &cfg)
			if cfg.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", cfg.Port, tt.wantPort)
			}
			gotWarning := len(warnings) > 0
			if gotWarning != tt.wantWarning {
				t.Errorf("got warning = %v, want %v (warnings: %v)", gotWarning, tt.wantWarning, warnings)
			}
		})
	}
}

func TestParseKeyValueWithWarnings_UintField(t *testing.T) {
	type C struct {
		Count uint `config:"count"`
	}
	tests := []struct {
		name        string
		input       string
		wantCount   uint
		wantWarning bool
	}{
		{"valid uint", "count: 42", 42, false},
		{"negative", "count: -1", 0, true},
		{"not a number", "count: hello", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			warnings := ParseKeyValueWithWarnings(tt.input, &cfg)
			if cfg.Count != tt.wantCount {
				t.Errorf("Count = %d, want %d", cfg.Count, tt.wantCount)
			}
			gotWarning := len(warnings) > 0
			if gotWarning != tt.wantWarning {
				t.Errorf("got warning = %v, want %v (warnings: %v)", gotWarning, tt.wantWarning, warnings)
			}
		})
	}
}

func TestParseKeyValueWithWarnings_BoolField(t *testing.T) {
	type C struct {
		Enabled bool `config:"enabled"`
	}
	tests := []struct {
		name        string
		input       string
		want        bool
		wantWarning bool
	}{
		{"true", "enabled: true", true, false},
		{"false", "enabled: false", false, false},
		{"yes", "enabled: yes", true, false},
		{"no", "enabled: no", false, false},
		{"invalid", "enabled: maybe", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			warnings := ParseKeyValueWithWarnings(tt.input, &cfg)
			if cfg.Enabled != tt.want {
				t.Errorf("Enabled = %v, want %v", cfg.Enabled, tt.want)
			}
			gotWarning := len(warnings) > 0
			if gotWarning != tt.wantWarning {
				t.Errorf("got warning = %v, want %v (warnings: %v)", gotWarning, tt.wantWarning, warnings)
			}
		})
	}
}

func TestParseKeyValueWithWarnings_FloatField(t *testing.T) {
	type C struct {
		Rate float64 `config:"rate"`
	}
	tests := []struct {
		name        string
		input       string
		want        float64
		wantWarning bool
	}{
		{"valid float", "rate: 3.14", 3.14, false},
		{"invalid float", "rate: xyz", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			warnings := ParseKeyValueWithWarnings(tt.input, &cfg)
			if cfg.Rate != tt.want {
				t.Errorf("Rate = %v, want %v", cfg.Rate, tt.want)
			}
			gotWarning := len(warnings) > 0
			if gotWarning != tt.wantWarning {
				t.Errorf("got warning = %v, want %v (warnings: %v)", gotWarning, tt.wantWarning, warnings)
			}
		})
	}
}

func TestParseKeyValueWithWarnings_TimeField(t *testing.T) {
	type C struct {
		CreatedAt time.Time `config:"created_at"`
	}
	tests := []struct {
		name        string
		input       string
		wantWarning bool
	}{
		{"valid time", "created_at: 2024-01-15T10:30:00Z", false},
		{"invalid time", "created_at: not-a-time", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			warnings := ParseKeyValueWithWarnings(tt.input, &cfg)
			gotWarning := len(warnings) > 0
			if gotWarning != tt.wantWarning {
				t.Errorf("got warning = %v, want %v (warnings: %v)", gotWarning, tt.wantWarning, warnings)
			}
		})
	}
}

func TestParseKeyValueWithWarnings_DurationField(t *testing.T) {
	type C struct {
		Timeout time.Duration `config:"timeout"`
	}
	tests := []struct {
		name        string
		input       string
		wantWarning bool
	}{
		{"valid duration", "timeout: 5s", false},
		{"invalid duration", "timeout: forever", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			warnings := ParseKeyValueWithWarnings(tt.input, &cfg)
			gotWarning := len(warnings) > 0
			if gotWarning != tt.wantWarning {
				t.Errorf("got warning = %v, want %v (warnings: %v)", gotWarning, tt.wantWarning, warnings)
			}
		})
	}
}

func TestParseKeyValueWithWarnings_MultipleWarnings(t *testing.T) {
	type C struct {
		Port    int     `config:"port"`
		Rate    float64 `config:"rate"`
		Enabled bool    `config:"enabled"`
	}
	input := `port: abc
rate: xyz
enabled: maybe`
	var cfg C
	warnings := ParseKeyValueWithWarnings(input, &cfg)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestParseKeyValueWithWarnings_NoWarningOnValidInput(t *testing.T) {
	type C struct {
		Name    string `config:"name"`
		Port    int    `config:"port"`
		Enabled bool   `config:"enabled"`
	}
	input := `name: "test"
port: 8080
enabled: true`
	var cfg C
	warnings := ParseKeyValueWithWarnings(input, &cfg)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
	if cfg.Name != "test" || cfg.Port != 8080 || !cfg.Enabled {
		t.Errorf("unexpected values: %+v", cfg)
	}
}

func TestParseWarningString(t *testing.T) {
	w := ParseWarning{Key: "port", Value: "abc", Err: "invalid integer"}
	s := w.String()
	if s != `key "port": cannot parse value "abc": invalid integer` {
		t.Errorf("unexpected string: %s", s)
	}
}
