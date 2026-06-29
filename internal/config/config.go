// Package config parses CLI flags and configuration files.
package config

import (
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Reasoning level constants.
// 0 = off (no reasoning), 1 = normal, 2 = max.
const (
	ReasoningLevelOff     = 0
	ReasoningLevelNormal  = 1
	ReasoningLevelMax     = 2
	DefaultReasoningLevel = ReasoningLevelNormal
)

// Agent behavior defaults.
const (
	DefaultMaxSteps = 0 // 0 means no limit; only bounded when user passes --max-steps

	// boolFalse is used for flag default comparison in printDefaults.
	boolFalse = "false"
)

// defaultConfigDir returns the default configuration directory (~/.alayacore).
func defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".alayacore")
}

// ExpandPath expands ~ to the user's home directory.
func ExpandPath(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	usr, err := user.Current()
	if err != nil {
		return path
	}
	if path == "~" {
		return usr.HomeDir
	}
	return filepath.Join(usr.HomeDir, path[1:])
}

// printDefaults prints all command-line flags with -- prefix instead of the default -
func printDefaults() {
	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		var placeholder string
		usage := f.Usage
		if s, _ := flag.UnquoteUsage(f); s != "" {
			placeholder = " " + s
		}
		usage = strings.ReplaceAll(usage, "`", "")
		if f.DefValue != "" && f.DefValue != boolFalse {
			fmt.Fprintf(flag.CommandLine.Output(), "\t--%s%s (default: %s)\n", f.Name, placeholder, f.DefValue)
		} else {
			fmt.Fprintf(flag.CommandLine.Output(), "\t--%s%s\n", f.Name, placeholder)
		}
		fmt.Fprintf(flag.CommandLine.Output(), "\t\t%s\n", usage)
	})
}

// stringSlice implements flag.Value for multiple string flags
type stringSlice struct {
	slice []string
}

func (s *stringSlice) String() string {
	return strings.Join(s.slice, ",")
}

func (s *stringSlice) Set(value string) error {
	s.slice = append(s.slice, value)
	return nil
}

func (s *stringSlice) Get() []string {
	return s.slice
}

// Settings holds all CLI configuration
type Settings struct {
	// Core
	ShowVersion   bool
	PlainIO       bool
	RawIO         bool
	DebugAPI      bool
	DebugMCP      bool
	ModelConfig   string // derived from config-path + "model.conf"
	RuntimeConfig string // derived from config-path + "runtime.conf"
	ThemesFolder  string // derived from config-path + "themes"
	Skills        []string
	Session       string

	// Model selection
	ModelName string

	// I/O
	Proxy string

	// Agent behavior
	SystemPrompt  string
	MaxSteps      int
	AutoSummarize bool
	ToolConfirm   []string // tool names requiring user confirmation

	// MCP (Model Context Protocol) servers
	MCPServers []string // raw flag values, parsed later
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	// Set custom usage function before any flag definitions
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), "AlayaCore - A minimal AI Agent\n\nUsage:\n\talayacore [flags]\n\nFlags:\n")
		printDefaults()
	}

	// Pre-compute default paths so they appear in --help output
	defaultConfigPath := defaultConfigDir()

	// Core
	showVersion := flag.Bool("version", false, "Show version information")
	plainIO := flag.Bool("plainio", false, "Use plain stdin/stdout mode instead of terminal UI")
	rawIO := flag.Bool("rawio", false, "Use raw TLV stdin/stdout mode instead of terminal UI (pipe TLV frames directly)")
	debugAPI := flag.Bool("debug-api", false, "Write raw API requests and responses to log file")
	debugMCP := flag.Bool("debug-mcp", false, "Write raw MCP JSON-RPC messages to log file")
	configPath := flag.String("config-path", defaultConfigPath, "Config directory `path` (contains model.conf, runtime.conf, themes/)")
	modelName := flag.String("model", "", "Model `name` to activate (must exist in model config; overrides runtime config)")
	skill := &stringSlice{}
	flag.Var(skill, "skill", "Skill `path` (can be specified multiple times)")
	session := flag.String("session", "", "Session file `path` to load/save conversations")

	// I/O
	proxy := flag.String("proxy", "", "HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")

	// Agent behavior
	systemPrompt := &stringSlice{}
	flag.Var(systemPrompt, "system", "Extra `system-prompt` (can be specified multiple times, will be appended to default)")
	maxSteps := flag.Int("max-steps", DefaultMaxSteps, "Maximum agent loop steps (0 = no limit)")
	autoSummarize := flag.Bool("auto-summarize", false, "Automatically summarize conversation when context exceeds 65% of limit")
	toolConfirm := flag.String("tool-confirm", "", "Comma-separated tool `names` requiring user confirmation (e.g. execute_command,search_content)")

	// MCP
	mcpServer := &stringSlice{}
	flag.Var(mcpServer, "mcp-server", "MCP server config: `name=command arg1 arg2` for stdio, `name@url` for SSE (can be specified multiple times)")

	flag.Parse()

	// Derive config file paths from config directory
	cp := *configPath
	s := &Settings{
		ShowVersion:   *showVersion,
		PlainIO:       *plainIO,
		RawIO:         *rawIO,
		DebugAPI:      *debugAPI,
		DebugMCP:      *debugMCP,
		ModelConfig:   filepath.Join(cp, "model.conf"),
		RuntimeConfig: filepath.Join(cp, "runtime.conf"),
		ThemesFolder:  filepath.Join(cp, "themes"),
		Skills:        skill.Get(),
		Session:       *session,
		ModelName:     *modelName,
		Proxy:         *proxy,
		SystemPrompt:  mergedSystemPrompt(systemPrompt),
		MaxSteps:      *maxSteps,
		AutoSummarize: *autoSummarize,
		ToolConfirm:   parseToolConfirm(*toolConfirm),
		MCPServers:    mcpServer.Get(),
	}

	return s
}

// mergedSystemPrompt joins multiple --system values with "\n\n".
func mergedSystemPrompt(sp *stringSlice) string {
	prompts := sp.Get()
	if len(prompts) == 0 {
		return ""
	}
	return strings.Join(prompts, "\n\n")
}

// parseToolConfirm splits a comma-separated tool-confirm value.
func parseToolConfirm(raw string) []string {
	if raw == "" {
		return nil
	}
	var names []string
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}
