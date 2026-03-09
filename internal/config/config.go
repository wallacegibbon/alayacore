package config

import (
	"flag"
	"strings"

	"github.com/wallacegibbon/coreclaw/internal/provider"
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

const Version = "0.1.0"

// Settings holds all CLI configuration
type Settings struct {
	ShowVersion  bool
	ShowHelp     bool
	DebugAPI     bool
	SystemPrompt string
	APIKey       string
	BaseURL      string
	ModelName    string
	ProviderType string
	Skills       []string
	Addr         string
	Session      string
	Proxy        string
}

// Parse parses CLI flags and returns settings
func Parse() *Settings {
	showVersion := flag.Bool("version", false, "Show version information")
	showHelp := flag.Bool("help", false, "Show help information")
	debugAPI := flag.Bool("debug-api", false, "Write raw API requests and responses to log file")
	systemPrompt := flag.String("system", "", "Override system prompt")
	apiKey := flag.String("api-key", "", "API key for the provider (required when using --base-url)")
	baseURL := flag.String("base-url", "", "Base URL for the API endpoint (requires --api-key, ignores env vars)")
	modelName := flag.String("model", "", "Model name to use (defaults to provider default)")
	providerType := flag.String("type", "", "Provider type: anthropic, openai (overrides auto-detection)")
	skill := &stringSlice{}
	flag.Var(skill, "skill", "Skill path (can be specified multiple times)")
	addr := flag.String("addr", ":8080", "Server address to listen on (for web server)")
	session := flag.String("session", "", "Session file path to load/save conversations")
	proxy := flag.String("proxy", "", "HTTP proxy URL (e.g., http://127.0.0.1:7890 or socks5://127.0.0.1:1080)")
	flag.Parse()

	// Collect skill paths
	skillPaths := skill.Get()

	s := &Settings{
		ShowVersion:  *showVersion,
		ShowHelp:     *showHelp,
		DebugAPI:     *debugAPI,
		SystemPrompt: *systemPrompt,
		APIKey:       *apiKey,
		BaseURL:      *baseURL,
		ModelName:    *modelName,
		ProviderType: *providerType,
		Skills:       skillPaths,
		Addr:         *addr,
		Session:      *session,
		Proxy:        *proxy,
	}

	return s
}

// GetProviderConfig returns the provider configuration
func (s *Settings) GetProviderConfig() (*provider.Config, error) {
	return provider.GetProviderConfig(s.APIKey, s.BaseURL, s.ModelName, s.ProviderType)
}
