package config

import (
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Think level constants.
// 0 = off (no reasoning), 1 = normal, 2 = max.
const (
	ThinkLevelOff     = 0
	ThinkLevelNormal  = 1
	ThinkLevelMax     = 2
	DefaultThinkLevel = ThinkLevelNormal
)

// Agent behavior defaults.
const (
	DefaultMaxSteps = 0 // 0 means no limit; only bounded when user passes --max-steps

	// boolFalse is used for flag default comparison in printDefaults.
	boolFalse = "false"
)

// ResolveConfigPath returns the provided path, or the default ~/.alayacore/<filename>
func ResolveConfigPath(providedPath, defaultFilename string) string {
	if providedPath != "" {
		return providedPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".alayacore", defaultFilename)
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
	DebugAPI      bool
	ModelConfig   string
	RuntimeConfig string
	ThemesFolder  string
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
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	// Set custom usage function before any flag definitions
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), "AlayaCore - A minimal AI Agent\n\nUsage:\n\talayacore [flags]\n\nFlags:\n")
		printDefaults()
	}

	// Pre-compute default paths so they appear in --help output
	defaultModelConfig := ResolveConfigPath("", "model.conf")
	defaultRuntimeConfig := ResolveConfigPath("", "runtime.conf")
	defaultThemesFolder := ResolveConfigPath("", "themes")

	// Core
	showVersion := flag.Bool("version", false, "Show version information")
	plainIO := flag.Bool("plainio", false, "Use plain stdin/stdout mode instead of terminal UI")
	debugAPI := flag.Bool("debug-api", false, "Write raw API requests and responses to log file")
	modelConfig := flag.String("model-config", defaultModelConfig, "Model config file `path`")
	runtimeConfig := flag.String("runtime-config", defaultRuntimeConfig, "Runtime config file `path`")
	themesFolder := flag.String("themes", defaultThemesFolder, "Themes folder `path`")
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

	flag.Parse()

	// Collect skill paths
	skillPaths := skill.Get()

	// Merge system prompts with "\n\n" separator
	var mergedSystemPrompt string
	systemPrompts := systemPrompt.Get()
	if len(systemPrompts) > 0 {
		mergedSystemPrompt = strings.Join(systemPrompts, "\n\n")
	}

	s := &Settings{
		ShowVersion:   *showVersion,
		PlainIO:       *plainIO,
		DebugAPI:      *debugAPI,
		ModelConfig:   *modelConfig,
		RuntimeConfig: *runtimeConfig,
		ThemesFolder:  *themesFolder,
		Skills:        skillPaths,
		Session:       *session,
		ModelName:     *modelName,
		Proxy:         *proxy,
		SystemPrompt:  mergedSystemPrompt,
		MaxSteps:      *maxSteps,
		AutoSummarize: *autoSummarize,
	}

	return s
}
