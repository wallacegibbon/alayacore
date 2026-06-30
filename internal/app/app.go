package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		parseWarns := config.ParseKeyValueWithWarnings(block, &fileCfg)
		for _, w := range parseWarns {
			warnings = append(warnings, fmt.Sprintf("mcp.conf: %s", w.String()))
		}

		if fileCfg.Server == "" {
			warnings = append(warnings, "mcp.conf: skipping block with empty server name")
			continue
		}

		configs = append(configs, fileCfg.ToServerConfig())
	}

	return configs, warnings
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

	// MCP manager for lifecycle management (cleanup).
	MCPManager       *mcp.Manager
	MCPStartupErrors []string // Non-fatal errors from MCP startup, displayed via adapter
}

// Setup initializes the common app components
func Setup(cfg *config.Settings) (*Config, error) {
	skillsManager, err := skills.NewManager(cfg.Skills)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize skills: %w", err)
	}

	agentTools, err := tools.DefaultTools(cfg.BuiltinTools)
	if err != nil {
		return nil, fmt.Errorf("invalid --builtin-tools: %w", err)
	}

	// ========================================================================
	// MCP (Model Context Protocol) initialization
	// ========================================================================
	mcpManager, mcpTools, mcpResourcesCtx, mcpPromptsCtx, mcpErrors := initMCP(cfg)

	agentTools = append(agentTools, mcpTools...)

	// ========================================================================
	// System Prompt Construction
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

	// Append MCP server instructions (hints from servers about how to use
	// their tools/resources effectively).
	if mcpManager != nil {
		for serverName, instructions := range mcpManager.ServerInstructions() {
			systemPrompt += fmt.Sprintf("\n\nInstructions from MCP server %q:\n%s", serverName, instructions)
		}
	}

	// Append pre-fetched resource and prompt lists so the LLM knows what
	// resources and prompts are available without needing to discover them.
	if mcpResourcesCtx != "" {
		systemPrompt += mcpResourcesCtx
	}
	if mcpPromptsCtx != "" {
		systemPrompt += mcpPromptsCtx
	}

	return &Config{
		Cfg:               cfg,
		SkillsMgr:         skillsManager,
		AgentTools:        agentTools,
		SystemPrompt:      systemPrompt,
		ExtraSystemPrompt: cfg.SystemPrompt,
		MaxSteps:          cfg.MaxSteps,
		ToolConfirmTools:  cfg.ToolConfirm,
		MCPManager:        mcpManager,
		MCPStartupErrors:  mcpErrors,
	}, nil
}

// initMCP initializes MCP servers from mcp.conf, connects, discovers tools,
// and returns the discovered MCP tools. Warnings are returned for the caller
// to display through the adapter (not printed here).
//
// It also pre-fetches resource and prompt lists from connected servers and
// returns them as formatted strings for injection into the system prompt.
func initMCP(cfg *config.Settings) (*mcp.Manager, []llm.Tool, string, string, []string) {
	// Load MCP configurations from mcp.conf
	mcpConfigs, startupErrors := loadMCPConfigs(cfg)
	if len(mcpConfigs) == 0 {
		return nil, nil, "", "", startupErrors
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

	mcpManager := mcp.NewManager(mcpConfigs)

	// Connect with a per-server timeout.
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 30*time.Second)
	connErrs := mcpManager.ConnectAll(connectCtx)
	connectCancel()

	for _, connErr := range connErrs {
		// Servers needing interactive auth are expected — skip them here,
		// they'll be handled by the adapter's handlePendingAuth().
		if errors.Is(connErr, mcp.ErrNeedsAuth) {
			continue
		}
		startupErrors = append(startupErrors, fmt.Sprintf("MCP server connection failed: %v", connErr))
	}

	var mcpTools []llm.Tool

	// Discover tools from connected servers.
	discoverCtx, discoverCancel := context.WithTimeout(context.Background(), 15*time.Second)
	serverTools := mcpManager.DiscoverTools(discoverCtx)
	discoverCancel()

	if len(serverTools) > 0 {
		mcpTools = append(mcpTools, mcp.ToolsToAgentTools(serverTools, mcpManager)...)
	}

	// Inject read_resource tools for servers that support Resources.
	resourceTools := mcp.ResourcesToAgentTools(mcpManager.Clients(), mcpManager)
	mcpTools = append(mcpTools, resourceTools...)

	// Inject get_prompt tools for servers that support Prompts.
	promptTools := mcp.PromptsToAgentTools(mcpManager.Clients(), mcpManager)
	mcpTools = append(mcpTools, promptTools...)

	// Pre-fetch resource and prompt lists from connected servers.
	listCtx, listCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer listCancel()

	resCtx := buildResourcesContext(listCtx, mcpManager)
	promptCtx := buildPromptsContext(listCtx, mcpManager)

	return mcpManager, mcpTools, resCtx, promptCtx, startupErrors
}

// buildResourcesContext fetches the resource list from all connected servers
// and returns a formatted string suitable for injection into the system prompt.
func buildResourcesContext(ctx context.Context, m *mcp.Manager) string {
	serverResources := m.DiscoverResources(ctx)
	if len(serverResources) == 0 {
		return ""
	}

	var b strings.Builder
	for serverName, resources := range serverResources {
		b.WriteString(fmt.Sprintf("\n\nAvailable resources from MCP server %q:", serverName))
		for _, r := range resources {
			b.WriteString(fmt.Sprintf("\n  - %s", r.URI))
			if r.Name != "" {
				b.WriteString(fmt.Sprintf(" (name: %q", r.Name))
				if r.Description != "" {
					b.WriteString(fmt.Sprintf(", description: %q", r.Description))
				}
				if r.MIMEType != "" {
					b.WriteString(fmt.Sprintf(", mimeType: %q", r.MIMEType))
				}
				b.WriteString(")")
			} else if r.Description != "" {
				b.WriteString(fmt.Sprintf(" (description: %q)", r.Description))
			}
		}
	}
	return b.String()
}

// buildPromptsContext fetches the prompt list from all connected servers
// and returns a formatted string suitable for injection into the system prompt.
func buildPromptsContext(ctx context.Context, m *mcp.Manager) string {
	serverPrompts := m.DiscoverPrompts(ctx)
	if len(serverPrompts) == 0 {
		return ""
	}

	var b strings.Builder
	for serverName, prompts := range serverPrompts {
		b.WriteString(fmt.Sprintf("\n\nAvailable prompts from MCP server %q:", serverName))
		for _, p := range prompts {
			b.WriteString(fmt.Sprintf("\n  - %s", p.Name))
			if p.Description != "" {
				b.WriteString(fmt.Sprintf(" (description: %q)", p.Description))
			}
			if len(p.Arguments) > 0 {
				b.WriteString("\n    Arguments:")
				for _, a := range p.Arguments {
					required := ""
					if a.Required {
						required = " (required)"
					}
					b.WriteString(fmt.Sprintf("\n      - %s: %s%s", a.Name, a.Description, required))
				}
			}
		}
	}
	return b.String()
}

// createTokenStore creates a FileTokenStore for persisting MCP OAuth tokens.
// Returns nil if the config directory cannot be determined.
func createTokenStore(cfg *config.Settings) *auth.FileTokenStore {
	// Derive token directory from the config path directory (parent of mcp.conf).
	tokenDir := filepath.Join(filepath.Dir(cfg.MCPConfigPath), "mcp-tokens")
	return auth.NewFileTokenStore(tokenDir)
}
