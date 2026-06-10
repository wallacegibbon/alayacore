package agent

// Type definitions for the session package.
// Kept separate for readability — no logic, just data structures.

import (
	"io"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/theme"
)

// QueueItem represents a queued task with metadata.
type QueueItem struct {
	QueueID   string    `json:"queue_id"`
	Type      string    `json:"type"` // "prompt" or "command"
	Content   string    `json:"content"`
	Images    []string  `json:"images,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Task type constants for QueueItem.Type.
const (
	TaskTypePrompt  = "prompt"
	TaskTypeCommand = "command"
)

// ============================================================================
// TagSystemMsg (SM) payload types
// ============================================================================

// TaskMsg carries task progress info (type "task").
type TaskMsg struct {
	InProgress  bool        `json:"in_progress"`
	CurrentStep int         `json:"current_step,omitempty"`
	MaxSteps    int         `json:"max_steps,omitempty"`
	Context     int64       `json:"context"`
	TaskError   bool        `json:"task_error,omitempty"`
	QueueItems  []QueueItem `json:"queue_items"`
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
	Models          []ModelInfo `json:"models"`
	ModelConfigPath string      `json:"model_config_path,omitempty"`
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
const MessageVersion = 4

// SessionMeta is the frontmatter metadata.
type SessionMeta struct {
	CreatedAt      time.Time `config:"created_at"`
	UpdatedAt      time.Time `config:"updated_at"`
	ReasoningLevel int       `config:"reasoning_level"`
	ActiveModel    string    `config:"active_model,omitempty"`
	ContextTokens  int64     `config:"context_tokens,omitempty"`
	MessageVersion int       `config:"message_version,omitempty"`
}

// SessionData is the persisted form of a Session.
type SessionData struct {
	SessionMeta
	Messages  []llm.Message
	TLVChunks []TLVChunk // Parsed TLV for direct display (avoids reconstruction)
}

// TLVChunk represents a single TLV message for display.
type TLVChunk struct {
	Tag   string
	Value string
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

	// Override
	OverrideActiveModel string // If set, overrides the active model (must exist in model config)
}
