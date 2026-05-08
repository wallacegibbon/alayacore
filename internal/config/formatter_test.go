package config

import (
	"strings"
	"testing"
	"time"
)

func TestFormatKeyValue_Basic(t *testing.T) {
	type Cfg struct {
		Name  string `config:"name"`
		Count int    `config:"count"`
	}

	out := FormatKeyValue(Cfg{Name: "hello", Count: 42})
	if !strings.Contains(out, `name: "hello"`) {
		t.Errorf("expected quoted string, got: %s", out)
	}
	if !strings.Contains(out, "count: 42") {
		t.Errorf("expected unquoted int, got: %s", out)
	}
}

func TestFormatKeyValue_OmitEmpty(t *testing.T) {
	type Cfg struct {
		Name   string `config:"name,omitempty"`
		Count  int    `config:"count,omitempty"`
		Always string `config:"always"`
	}

	out := FormatKeyValue(Cfg{Name: "", Count: 0, Always: "yes"})
	if strings.Contains(out, "name:") {
		t.Errorf("expected name to be omitted, got: %s", out)
	}
	if strings.Contains(out, "count:") {
		t.Errorf("expected count to be omitted, got: %s", out)
	}
	if !strings.Contains(out, `always: "yes"`) {
		t.Errorf("expected always to be present, got: %s", out)
	}
}

func TestFormatKeyValue_Time(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	type Cfg struct {
		Created time.Time `config:"created"`
	}

	out := FormatKeyValue(Cfg{Created: ts})
	if !strings.Contains(out, "created: 2024-01-15T10:30:00Z") {
		t.Errorf("expected RFC3339 time, got: %s", out)
	}
}

func TestFormatKeyValue_RoundTrip(t *testing.T) {
	type Cfg struct {
		Name  string `config:"name"`
		Count int    `config:"count"`
		Flag  bool   `config:"flag"`
	}

	original := Cfg{Name: "test", Count: 7, Flag: true}
	out := FormatKeyValue(original)

	var parsed Cfg
	ParseKeyValue(out, &parsed)

	if parsed.Name != original.Name {
		t.Errorf("name: got %q, want %q", parsed.Name, original.Name)
	}
	if parsed.Count != original.Count {
		t.Errorf("count: got %d, want %d", parsed.Count, original.Count)
	}
	if parsed.Flag != original.Flag {
		t.Errorf("flag: got %v, want %v", parsed.Flag, original.Flag)
	}
}

func TestFormatKeyValue_StringEscaping(t *testing.T) {
	type Cfg struct {
		Path string `config:"path"`
	}

	out := FormatKeyValue(Cfg{Path: `say "hi" \n`})
	if !strings.Contains(out, `path: "say \"hi\" \\n"`) {
		t.Errorf("expected escaped string, got: %s", out)
	}
}
