package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/mcp/auth"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/tools"
)

// loadMCPConfigs reads mcp.conf from the config directory and parses it
// into a slice of ServerConfig. Returns any parse/load errors as warnings.
func loadMCPConfigs(cfg *config.Settings) ([]mcp.ServerConfig, []string) {
	data, err := os.ReadFile(cfg.MCPConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // mcp.conf is optional
		}
		return nil, []string{fmt.Sprintf("reading mcp.conf: %v", err)}
	}

	blocks := config.ParseKeyValueBlocks(string(data))
	configs := make([]mcp.ServerConfig, 0, len(blocks))
	var warnings []string

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, "#") {
			continue
		}

		var fileCfg mcp.ServerConfigFile
		parseWarns := config.ParseKeyValue(block, &fileCfg)
		for _, w := range parseWarns {
			warnings = append(warnings, fmt.Sprintf("mcp.conf: %s", w.String()))
		}

		if fileCfg.Server == "" {
			warnings = append(warnings, "mcp.conf: skipping block with empty server name")
			continue
		}

		configs = append(configs, fileCfg.ToServerConfig())
	}

	// Check for duplicate server names.
	// First occurrence is kept; subsequent duplicates are reported as errors.
	seenNames := make(map[string]bool)
	deduped := make([]mcp.ServerConfig, 0, len(configs))
	for _, cfg := range configs {
		if seenNames[cfg.Name] {
			warnings = append(warnings, fmt.Sprintf("mcp.conf: duplicate server name %q — skipped", cfg.Name))
			continue
		}
		seenNames[cfg.Name] = true
		deduped = append(deduped, cfg)
	}

	return deduped, warnings
}

// This package provides shared initialization for all adapters.
// It builds the system prompt, initializes tools, and creates the app config.

const systemPromptBase = `You are a helpful AI assistant with access to a set of tools that you can use to accomplish tasks.

Never assume - verify with tools.

Use search tools to locate code and patterns before using file read tools for detailed inspection.`

const systemPromptSkills = `Check <available_skills> below; read the <location> file to load relevant skill instructions. Skill instructions may use relative paths - run them from the skill's directory (derived from <location>).`

// Config holds the common app configuration
type Config struct {
	Cfg               *config.Settings
	SkillsMgr         *skills.Manager
	AgentTools        []llm.Tool
	SystemPrompt      string   // Default system prompt (always present)
	ExtraSystemPrompt string   // User-provided extra system prompt via --system flag
	MaxSteps          int      // Maximum agent loop steps
	ToolConfirmTools  []string // Tool names requiring user confirmation

	// MCPInit provides asynchronous MCP initialization.
	// If non-nil, MCP servers are configured and initialization is
	// running in the background. The session manages init results
	// internally — the adapter receives progress via system messages.
	MCPInit *mcp.Init

	// StartupErrors contains non-fatal warnings from theme loading,
	// runtime config parsing, and MCP config parsing. These are
	// emitted as TLV system messages during session startup so the
	// user sees them even in TUI mode.
	StartupErrors []string
}

// Setup initializes the common app components.
//
// Fast path: skills, built-in tools, MCP config parsing (no connections).
// MCP initialization (connect, discover) runs asynchronously via cfg.AsyncMCP.
// The session manages init results internally — the adapter only needs
// AsyncMCP for TUI lifecycle checks (e.g. init overlay).
func Setup(cfg *config.Settings) (*Config, error) {
	skillsManager, err := skills.NewManager(cfg.Skills)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	agentTools, err := tools.DefaultTools(cfg.BuiltinTools)
	if err != nil {
		return nil, fmt.Errorf("invalid --builtin-tools: %w", err)
	}

	// Collect non-fatal startup warnings from all sources.
	var startupErrors []string

	// ========================================================================
	// MCP (Model Context Protocol) — async initialization
	// ========================================================================
	mcpInit, mcpErrors := initMCPAsync(cfg)
	startupErrors = append(startupErrors, mcpErrors...)

	// ========================================================================
	// System Prompt Construction (base — without MCP sections)
	// ========================================================================

	// Build the default system prompt
	systemPrompt := systemPromptBase

	// Only include SKILLS section when skills are actually available
	skillsFragment := skillsManager.GenerateSystemPromptFragment()
	if skillsFragment != "" {
		systemPrompt = systemPrompt + "\n\n" + systemPromptSkills + "\n\n" + skillsFragment
	}

	// Append CWD at the end so the LLM constructs correct absolute paths
	// from the first tool call. Placed last for API cache reuse — stable
	// portion stays cached, only the suffix changes per project.
	// See docs/architecture.md for rationale.
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		systemPrompt = systemPrompt + "\n\nCurrent working directory: " + cwd
	}

	// Note: MCP sections (instructions, resources, prompts) are NOT added here.
	// They'll be appended dynamically when async MCP init completes —
	// the session applies them internally via applyMCPUpdate.

	return &Config{
		Cfg:               cfg,
		SkillsMgr:         skillsManager,
		AgentTools:        agentTools, // no MCP tools yet
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: cfg.SystemPrompt,
		MaxSteps:          cfg.MaxSteps,
		ToolConfirmTools:  cfg.ToolConfirm,
		MCPInit:           mcpInit,
		StartupErrors:     startupErrors,
	}, nil
}

// initMCPAsync starts asynchronous MCP initialization.
// Returns an Init (nil if no MCP servers configured) and any config
// parsing warnings. The Init is NOT started yet — the session starts
// it when the main event loop begins.
func initMCPAsync(cfg *config.Settings) (*mcp.Init, []string) {
	// Load MCP configurations from mcp.conf
	mcpConfigs, startupErrors := loadMCPConfigs(cfg)
	if len(mcpConfigs) == 0 {
		return nil, startupErrors
	}

	// Set debug mode from global config.
	for i := range mcpConfigs {
		mcpConfigs[i].Debug = cfg.DebugMCP
	}

	// Set up token persistence for all MCP servers.
	if tokenStore := createTokenStore(cfg); tokenStore != nil {
		for i := range mcpConfigs {
			mcpConfigs[i].TokenStore = tokenStore
		}
	}

	mcpInit := mcp.NewInit(mcpConfigs)
	return mcpInit, startupErrors
}

// createTokenStore creates a FileTokenStore for persisting MCP OAuth tokens.
// Returns nil if the config directory cannot be determined.
func createTokenStore(cfg *config.Settings) *auth.FileTokenStore {
	// Derive token directory from the config path directory (parent of mcp.conf).
	tokenDir := filepath.Join(filepath.Dir(cfg.MCPConfigPath), "mcp-tokens")
	return auth.NewFileTokenStore(tokenDir)
}
