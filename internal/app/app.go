package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/tools"
)

// This package provides shared initialization for all adapters.
// It builds the system prompt, initializes tools, and creates the app config.

const systemPromptIdentity = `You are a helpful AI assistant with access to tools for reading/writing files and executing commands.`

const systemPromptRules = `Never assume - verify with tools.`

const systemPromptSearch = `Use search_content to locate code and patterns before using read_file for detailed inspection.`

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

	// MCP manager for lifecycle management (cleanup).
	MCPServerTools []llm.Tool // Tools from MCP servers (subset of AgentTools)
	MCPManager     *mcp.Manager
}

// Setup initializes the common app components
func Setup(cfg *config.Settings) (*Config, error) {
	skillsManager, err := skills.NewManager(cfg.Skills)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	agentTools := tools.DefaultTools()

	// ========================================================================
	// MCP (Model Context Protocol) initialization
	// ========================================================================
	mcpManager, mcpServerTools := initMCP(cfg, &agentTools)

	// ========================================================================
	// System Prompt Construction
	// ========================================================================

	// Build the default system prompt
	rgAvailable := tools.RGAvailable()
	var systemPrompt string
	if rgAvailable {
		systemPrompt = systemPromptIdentity + "\n\n" + systemPromptRules + "\n\n" + systemPromptSearch
	} else {
		systemPrompt = systemPromptIdentity + "\n\n" + systemPromptRules
	}

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

	return &Config{
		Cfg:               cfg,
		SkillsMgr:         skillsManager,
		AgentTools:        agentTools,
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: cfg.SystemPrompt,
		MaxSteps:          cfg.MaxSteps,
		ToolConfirmTools:  cfg.ToolConfirm,
		MCPServerTools:    mcpServerTools,
		MCPManager:        mcpManager,
	}, nil
}

// initMCP initializes MCP servers: parses configs, connects, discovers tools,
// and injects them into agentTools.
func initMCP(cfg *config.Settings, agentTools *[]llm.Tool) (*mcp.Manager, []llm.Tool) {
	if len(cfg.MCPServers) == 0 {
		return nil, nil
	}

	// Pre-allocate with the maximum possible size; unused capacity is negligible.
	mcpConfigs := make([]mcp.ServerConfig, 0, len(cfg.MCPServers))
	for _, raw := range cfg.MCPServers {
		parsed, parseErr := mcp.ParseServerConfig(raw)
		if parseErr != nil {
			log.Printf("Warning: invalid --mcp-server config %q: %v", raw, parseErr)
			continue
		}
		parsed.Debug = cfg.DebugMCP
		mcpConfigs = append(mcpConfigs, parsed)
	}

	if len(mcpConfigs) == 0 {
		return nil, nil
	}

	mcpManager := mcp.NewManager(mcpConfigs)

	// Connect with a per-server timeout.
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 30*time.Second)
	errs := mcpManager.ConnectAll(connectCtx)
	connectCancel()

	for _, connErr := range errs {
		log.Printf("Warning: MCP server connection failed: %v", connErr)
	}

	// Discover tools from connected servers.
	discoverCtx, discoverCancel := context.WithTimeout(context.Background(), 15*time.Second)
	serverTools := mcpManager.DiscoverTools(discoverCtx)
	discoverCancel()

	var mcpServerTools []llm.Tool

	if len(serverTools) > 0 {
		mcpServerTools = mcp.ToolsToAgentTools(serverTools, mcpManager)
		*agentTools = append(*agentTools, mcpServerTools...)
	}

	return mcpManager, mcpServerTools
}
