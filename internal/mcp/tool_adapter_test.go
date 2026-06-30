package mcp

import (
	"encoding/json"
	"testing"
)

func TestParseServerConfig_Stdio(t *testing.T) {
	tests := []struct {
		input   string
		want    ServerConfig
		wantErr bool
	}{
		{
			input: "db=exec:npx @anthropic/mcp-db-server",
			want: ServerConfig{
				Name:    "db",
				Command: "npx",
				Args:    []string{"@anthropic/mcp-db-server"},
			},
		},
		{
			input: "git=exec:uvx mcp-git",
			want: ServerConfig{
				Name:    "git",
				Command: "uvx",
				Args:    []string{"mcp-git"},
			},
		},
		{
			input:   "",
			wantErr: true,
		},
		{
			input:   "=exec:cmd",
			wantErr: true,
		},
		{
			input:   "name=npx", // no transport: prefix
			wantErr: true,
		},
		{
			input:   "db=exec:", // empty command
			wantErr: true,
		},
		// With environment variables.
		{
			input: "db=exec:DB_HOST=localhost DB_PORT=5432 npx @anthropic/mcp-db-server",
			want: ServerConfig{
				Name:    "db",
				Command: "npx",
				Args:    []string{"@anthropic/mcp-db-server"},
				Env:     map[string]string{"DB_HOST": "localhost", "DB_PORT": "5432"},
			},
		},
		{
			input: "git=exec:TOKEN=abc123 uvx mcp-git",
			want: ServerConfig{
				Name:    "git",
				Command: "uvx",
				Args:    []string{"mcp-git"},
				Env:     map[string]string{"TOKEN": "abc123"},
			},
		},
		{
			input:   "db=exec:ONLY_ENV=1", // no command after env
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseServerConfig(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseServerConfig() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Name != tt.want.Name {
					t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
				}
				if got.Command != tt.want.Command {
					t.Errorf("Command = %q, want %q", got.Command, tt.want.Command)
				}
				if len(got.Args) != len(tt.want.Args) {
					t.Errorf("Args = %v, want %v", got.Args, tt.want.Args)
				} else {
					for i := range got.Args {
						if got.Args[i] != tt.want.Args[i] {
							t.Errorf("Args[%d] = %q, want %q", i, got.Args[i], tt.want.Args[i])
						}
					}
				}
				if len(got.Env) != len(tt.want.Env) {
					t.Errorf("Env = %v, want %v", got.Env, tt.want.Env)
				} else {
					for k, v := range tt.want.Env {
						if got.Env[k] != v {
							t.Errorf("Env[%q] = %q, want %q", k, got.Env[k], v)
						}
					}
				}
			}
		})
	}
}

func TestParseServerConfig_HTTP(t *testing.T) {
	tests := []struct {
		input   string
		want    ServerConfig
		wantErr bool
	}{
		{
			input: "remote=https://mcp.example.com/sse",
			want: ServerConfig{
				Name: "remote",
				URL:  "https://mcp.example.com/sse",
			},
		},
		{
			input: "remote=http://example.com/mcp",
			want: ServerConfig{
				Name: "remote",
				URL:  "http://example.com/mcp",
			},
		},
		{
			input:   "name=",
			wantErr: true,
		},
		{
			input:   "=http://url",
			wantErr: true,
		},
		{
			input:   "name@url", // old @ format
			wantErr: true,
		},
		{
			input:   "name=ssh:host", // unknown format
			wantErr: true,
		},
		{
			input:   "name=ftp://host", // unsupported protocol
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseServerConfig(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseServerConfig() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Name != tt.want.Name {
					t.Errorf("Name = %q, want %q", got.Name, tt.want.Name)
				}
				if got.URL != tt.want.URL {
					t.Errorf("URL = %q, want %q", got.URL, tt.want.URL)
				}
			}
		})
	}
}

func TestBuildToolName(t *testing.T) {
	got := buildToolName("my server", "do_something", ToolNamePrefix)
	want := "my_server_do_something"
	if got != want {
		t.Errorf("buildToolName() = %q, want %q", got, want)
	}

	got = buildToolName("any", "bare_tool", ToolNameKeep)
	want = "bare_tool"
	if got != want {
		t.Errorf("buildToolName(keep) = %q, want %q", got, want)
	}
}

func TestBuildDescription(t *testing.T) {
	got := buildDescription("db", "Query the database", nil)
	if got != `MCP tool from server "db": Query the database` {
		t.Errorf("buildDescription() = %q", got)
	}

	got = buildDescription("db", "", nil)
	if got != `MCP tool from server "db"` {
		t.Errorf("buildDescription(empty) = %q", got)
	}

	// With annotations: ReadOnly + Idempotent, others use spec defaults.
	yes := true
	no := false
	got = buildDescription("db", "Query the database", &ToolAnnotations{
		ReadOnlyHint:   &yes,
		IdempotentHint: &yes,
	})
	want := `MCP tool from server "db": Query the database [Read-only, Destructive, Idempotent, Open-world]`
	if got != want {
		t.Errorf("buildDescription(with annotations) = %q, want %q", got, want)
	}

	// Destructive only (explicit true), OpenWorld uses spec default (true).
	got = buildDescription("git", "Delete branch", &ToolAnnotations{
		DestructiveHint: &yes,
	})
	want = `MCP tool from server "git": Delete branch [Destructive, Open-world]`
	if got != want {
		t.Errorf("buildDescription(destructive) = %q, want %q", got, want)
	}

	// All explicitly false → no hint appended (overrides spec defaults).
	got = buildDescription("db", "Query", &ToolAnnotations{
		ReadOnlyHint:    &no,
		DestructiveHint: &no,
		IdempotentHint:  &no,
		OpenWorldHint:   &no,
	})
	if got != `MCP tool from server "db": Query` {
		t.Errorf("buildDescription(all false) = %q, want without hint", got)
	}

	// Empty annotations (all nil) → spec defaults apply (Destructive + Open-world).
	got = buildDescription("db", "Query", &ToolAnnotations{})
	want = `MCP tool from server "db": Query [Destructive, Open-world]`
	if got != want {
		t.Errorf("buildDescription(empty annotations) = %q, want %q", got, want)
	}
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"simple", []string{"simple"}},
		{"two args", []string{"two", "args"}},
		{`quoted "long arg" here`, []string{"quoted", "long arg", "here"}},
		{`mixed 'single quoted' "double quoted"`, []string{"mixed", "single quoted", "double quoted"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitArgs(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitArgs() = %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestToolJSON(t *testing.T) {
	tool := Tool{
		Name:        "read_file",
		Description: "Read contents of a file",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Marshal Tool: %v", err)
	}

	var decoded Tool
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal Tool: %v", err)
	}

	if decoded.Name != tool.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, tool.Name)
	}
	if decoded.Description != tool.Description {
		t.Errorf("Description = %q, want %q", decoded.Description, tool.Description)
	}
}

func TestCallToolResultJSON(t *testing.T) {
	result := CallToolResult{
		Content: []ToolContent{
			{Type: "text", Text: "Hello from MCP"},
		},
		IsError: false,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal CallToolResult: %v", err)
	}

	var decoded CallToolResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal CallToolResult: %v", err)
	}

	if len(decoded.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(decoded.Content))
	}
	if decoded.Content[0].Text != "Hello from MCP" {
		t.Errorf("Text = %q, want %q", decoded.Content[0].Text, "Hello from MCP")
	}
}
