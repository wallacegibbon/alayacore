package config

import (
	"testing"
	"time"
)

func TestParseKeyValueWithErrors_IntField(t *testing.T) {
	type C struct {
		Port int `config:"port"`
	}
	tests := []struct {
		name     string
		input    string
		wantPort int
		wantErr  bool
	}{
		{"valid int", "port: 8080", 8080, false},
		{"invalid int", "port: abc", 0, true},
		{"empty value", "port: ", 0, false}, // empty is not a parse error for int, it just fails silently
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			errs := ParseKeyValue(tt.input, &cfg)
			if cfg.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", cfg.Port, tt.wantPort)
			}
			gotErr := len(errs) > 0
			if gotErr != tt.wantErr {
				t.Errorf("got error = %v, want %v (errors: %v)", gotErr, tt.wantErr, errs)
			}
		})
	}
}

func TestParseKeyValueWithErrors_UintField(t *testing.T) {
	type C struct {
		Count uint `config:"count"`
	}
	tests := []struct {
		name      string
		input     string
		wantCount uint
		wantErr   bool
	}{
		{"valid uint", "count: 42", 42, false},
		{"negative", "count: -1", 0, true},
		{"not a number", "count: hello", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			errs := ParseKeyValue(tt.input, &cfg)
			if cfg.Count != tt.wantCount {
				t.Errorf("Count = %d, want %d", cfg.Count, tt.wantCount)
			}
			gotErr := len(errs) > 0
			if gotErr != tt.wantErr {
				t.Errorf("got error = %v, want %v (errors: %v)", gotErr, tt.wantErr, errs)
			}
		})
	}
}

func TestParseKeyValueWithErrors_BoolField(t *testing.T) {
	type C struct {
		Enabled bool `config:"enabled"`
	}
	tests := []struct {
		name    string
		input   string
		want    bool
		wantErr bool
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
			errs := ParseKeyValue(tt.input, &cfg)
			if cfg.Enabled != tt.want {
				t.Errorf("Enabled = %v, want %v", cfg.Enabled, tt.want)
			}
			gotErr := len(errs) > 0
			if gotErr != tt.wantErr {
				t.Errorf("got error = %v, want %v (errors: %v)", gotErr, tt.wantErr, errs)
			}
		})
	}
}

func TestParseKeyValueWithErrors_FloatField(t *testing.T) {
	type C struct {
		Rate float64 `config:"rate"`
	}
	tests := []struct {
		name    string
		input   string
		want    float64
		wantErr bool
	}{
		{"valid float", "rate: 3.14", 3.14, false},
		{"invalid float", "rate: xyz", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			errs := ParseKeyValue(tt.input, &cfg)
			if cfg.Rate != tt.want {
				t.Errorf("Rate = %v, want %v", cfg.Rate, tt.want)
			}
			gotErr := len(errs) > 0
			if gotErr != tt.wantErr {
				t.Errorf("got error = %v, want %v (errors: %v)", gotErr, tt.wantErr, errs)
			}
		})
	}
}

func TestParseKeyValueWithErrors_TimeField(t *testing.T) {
	type C struct {
		CreatedAt time.Time `config:"created_at"`
	}
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid time", "created_at: 2024-01-15T10:30:00Z", false},
		{"invalid time", "created_at: not-a-time", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			errs := ParseKeyValue(tt.input, &cfg)
			gotErr := len(errs) > 0
			if gotErr != tt.wantErr {
				t.Errorf("got error = %v, want %v (errors: %v)", gotErr, tt.wantErr, errs)
			}
		})
	}
}

func TestParseKeyValueWithErrors_DurationField(t *testing.T) {
	type C struct {
		Timeout time.Duration `config:"timeout"`
	}
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid duration", "timeout: 5s", false},
		{"invalid duration", "timeout: forever", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg C
			errs := ParseKeyValue(tt.input, &cfg)
			gotErr := len(errs) > 0
			if gotErr != tt.wantErr {
				t.Errorf("got error = %v, want %v (errors: %v)", gotErr, tt.wantErr, errs)
			}
		})
	}
}

func TestParseKeyValueWithErrors_MultipleErrors(t *testing.T) {
	type C struct {
		Port    int     `config:"port"`
		Rate    float64 `config:"rate"`
		Enabled bool    `config:"enabled"`
	}
	input := `port: abc
rate: xyz
enabled: maybe`
	var cfg C
	errs := ParseKeyValue(input, &cfg)
	if len(errs) != 3 {
		t.Fatalf("expected 3 errors, got %d: %v", len(errs), errs)
	}
}

func TestParseKeyValueWithErrors_NoErrorOnValidInput(t *testing.T) {
	type C struct {
		Name    string `config:"name"`
		Port    int    `config:"port"`
		Enabled bool   `config:"enabled"`
	}
	input := `name: "test"
port: 8080
enabled: true`
	var cfg C
	errs := ParseKeyValue(input, &cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if cfg.Name != "test" || cfg.Port != 8080 || !cfg.Enabled {
		t.Errorf("unexpected values: %+v", cfg)
	}
}

func TestParseErrorString(t *testing.T) {
	e := ParseError{Key: "port", Value: "abc", Err: "invalid integer"}
	s := e.String()
	if s != `key "port": cannot parse value "abc": invalid integer` {
		t.Errorf("unexpected string: %s", s)
	}
}
