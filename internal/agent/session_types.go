package agent

// Type definitions for the session package.
// Kept separate for readability — no logic, just data structures.

import (
	"io"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/mcp"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/theme"
)

// ============================================================================
// TagSystemMsg (SM) payload types
// ============================================================================

// TaskMsg carries task progress info (type "task").
type TaskMsg struct {
	InProgress  bool  `json:"in_progress"`
	CurrentStep int   `json:"current_step,omitempty"`
	MaxSteps    int   `json:"max_steps,omitempty"`
	Context     int64 `json:"context"`
	TaskError   bool  `json:"task_error,omitempty"`
}

func (TaskMsg) SystemMsgType() string { return "task" }

// ModelMsg carries active model info (type "model").
type ModelMsg struct {
	ActiveModelID   int    `json:"active_id"`
	ActiveModelName string `json:"active_name"`
	ContextLimit    int64  `json:"context_limit"`
}

func (ModelMsg) SystemMsgType() string { return "model" }

// ModelListMsg carries the full model list (type "model_list").
// Only sent when models change.
type ModelListMsg struct {
	Models []ModelConfig `json:"models"`
}

func (ModelListMsg) SystemMsgType() string { return "model_list" }

// ThemeInfo carries a theme's name and full content for adapters.
type ThemeInfo struct {
	Name  string       `json:"name"`
	Theme *theme.Theme `json:"theme"`
}

// ThemeListMsg carries all available themes (type "theme_list").
// Sent once on startup so adapters can cache theme content locally.
type ThemeListMsg struct {
	Themes []ThemeInfo `json:"themes"`
}

func (ThemeListMsg) SystemMsgType() string { return "theme_list" }

// ThemeMsg carries the active theme name (type "theme").
// On startup the full Theme is included; on theme changes only the name is sent.
type ThemeMsg struct {
	Name  string       `json:"name"`
	Theme *theme.Theme `json:"theme,omitempty"`
}

func (ThemeMsg) SystemMsgType() string { return "theme" }

// ReasoningMsg carries the reasoning level (type "reasoning").
type ReasoningMsg struct {
	Level int `json:"level"`
}

func (ReasoningMsg) SystemMsgType() string { return "reasoning" }

// VideoConfigMsg carries the video FPS and resolution (type "video_config").
type VideoConfigMsg struct {
	FPS int `json:"fps"`
	Res int `json:"res"`
}

func (VideoConfigMsg) SystemMsgType() string { return "video_config" }

// MCPAuthServer describes an MCP server that needs OAuth authorization.
type MCPAuthServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MCPInitMsg communicates MCP initialization progress (type "mcp_init").
// The adapter uses these messages to show/hide init overlays without
// needing to poll AsyncMCP.Done().
//
// Status values:
//   - "starting":     async init goroutine has started
//   - "ready":        init complete, no OAuth needed
//   - "auth_required": init complete, OAuth servers pending (in PendingAuth)
type MCPInitMsg struct {
	Status      string          `json:"status"`
	ToolCount   int             `json:"tool_count,omitempty"`
	ServerCount int             `json:"server_count,omitempty"`
	PendingAuth []MCPAuthServer `json:"pending_auth,omitempty"`
}

func (MCPInitMsg) SystemMsgType() string { return "mcp_init" }

// MCPAuthMsg communicates MCP OAuth authorization progress (type "mcp_auth").
// Sent by the OAuth goroutine so the adapter can show a status overlay.
type MCPAuthMsg struct {
	Server string `json:"server"`
	Status string `json:"status"` // "in_progress", "done", "error"
	Error  string `json:"error,omitempty"`
}

func (MCPAuthMsg) SystemMsgType() string { return "mcp_auth" }

// MessageVersionMsg carries the TLV message format version (type "version").
// Sent as the first TagSystemMsg frame so adapters can validate format
// compatibility before processing subsequent messages.
type MessageVersionMsg struct {
	MessageVersion int `json:"message_version"`
}

func (MessageVersionMsg) SystemMsgType() string { return "version" }

// MessageVersion is the current version of the message encoding
// used in session files and TagSystemMsg broadcasts.
// Increment when making backward-incompatible changes to the TLV
// message format within the session body.
const MessageVersion = 8

// SessionMeta is the frontmatter metadata.
type SessionMeta struct {
	CreatedAt      time.Time `config:"created_at"`
	UpdatedAt      time.Time `config:"updated_at"`
	ActiveModel    string    `config:"active_model,omitempty"`
	MessageVersion int       `config:"message_version,omitempty"`
	ReasoningLevel int       `config:"reasoning_level"`
	ContextTokens  int64     `config:"context_tokens,omitempty"`
	VideoFPS       int       `config:"video_fps"`
	VideoRes       int       `config:"video_res"`
}

// taskResultCh carries the final content list from the task goroutine to run().

// SessionData is the persisted form of a Session.
type SessionData struct {
	SessionMeta
	Contents []llm.ContentPart // source of truth on reload
}

// SessionConfig bundles all configuration for creating or restoring a session.
// This avoids passing 16+ positional parameters to NewSession / RestoreFromSession.
type SessionConfig struct {
	// IO — required, provided by the adapter.
	Input  io.Reader
	Output io.Writer

	// Files — paths to configuration and session files. Empty means default / none.
	SessionFile       string
	ModelConfigPath   string
	RuntimeConfigPath string
	ThemesFolder      string

	// Agent behavior
	BaseTools         []llm.Tool
	SystemPrompt      string
	ExtraSystemPrompt string
	MaxSteps          int
	ToolConfirmTools  []string // tool names requiring user confirmation (empty = no confirmation)

	// Feature flags
	DebugAPI      bool
	AutoSummarize bool
	ProxyURL      string

	// External dependencies
	SkillsMgr *skills.Manager

	// MCP manager for tool execution and lifecycle management.
	// Set during async initialization; may be nil initially.
	MCPManager *mcp.Manager

	// AsyncInit provides asynchronous MCP initialization.
	// When non-nil, the session starts a goroutine that waits for
	// AsyncInit.Done() and applies the results (tools, system prompt,
	// manager) internally — no adapter involvement needed.
	AsyncInit *mcp.AsyncInit

	// Override
	OverrideActiveModel string // If set, overrides the active model (must exist in model config)
}
