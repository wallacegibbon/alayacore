package config

import (
	"flag"
	"strings"
)

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
	ShowVersion   bool
	ShowHelp      bool
	DebugAPI      bool
	SystemPrompt  string
	Skills        []string
	Addr          string
	Session       string
	Proxy         string
	ModelConfig   string
	RuntimeConfig string
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	showVersion := flag.Bool("version", false, "Show version information")
	showHelp := flag.Bool("help", false, "Show help information")
	debugAPI := flag.Bool("debug-api", false, "Write raw API requests and responses to log file")
	systemPrompt := flag.String("system", "", "Override system prompt")
	skill := &stringSlice{}
	flag.Var(skill, "skill", "Skill path (can be specified multiple times)")
	addr := flag.String("addr", ":8080", "Server address to listen on (for web server)")
	session := flag.String("session", "", "Session file path to load/save conversations")
	proxy := flag.String("proxy", "", "HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")
	modelConfig := flag.String("model-config", "", "Model config file path (default: ~/.alayacore/model.conf)")
	runtimeConfig := flag.String("runtime-config", "", "Runtime config file path (default: <model-config-dir>/runtime.conf, or ~/.alayacore/runtime.conf)")
	flag.Parse()

	// Collect skill paths
	skillPaths := skill.Get()

	s := &Settings{
		ShowVersion:   *showVersion,
		ShowHelp:      *showHelp,
		DebugAPI:      *debugAPI,
		SystemPrompt:  *systemPrompt,
		Skills:        skillPaths,
		Addr:          *addr,
		Session:       *session,
		Proxy:         *proxy,
		ModelConfig:   *modelConfig,
		RuntimeConfig: *runtimeConfig,
	}

	return s
}
