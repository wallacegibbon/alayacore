package agent

// Type definitions for the session package.
// Kept separate for readability — no logic, just data structures.

import (
	"io"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
)

// QueueItem represents a queued task with metadata.
type QueueItem struct {
	QueueID   string    `json:"queue_id"`
	Type      string    `json:"type"` // "prompt" or "command"
	Content   string    `json:"content"`
	Images    []string  `json:"images,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ============================================================================
// TagSystemMsg (SM) payload types
// ============================================================================

// TaskMsg carries task progress info (type "task").
type TaskMsg struct {
	InProgress   bool        `json:"in_progress"`
	CurrentStep  int         `json:"current_step,omitempty"`
	MaxSteps     int         `json:"max_steps,omitempty"`
	Context     int64  `json:"context"`
	TaskError   bool   `json:"task_error,omitempty"`
	QueueItems   []QueueItem `json:"queue_items"`
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

// ThemeMsg carries the active theme name (type "theme").
type ThemeMsg struct {
	Name string `json:"name"`
}

func (ThemeMsg) SystemMsgType() string { return "theme" }

// ReasoningMsg carries the reasoning level (type "reasoning").
type ReasoningMsg struct {
	Level int `json:"level"`
}

func (ReasoningMsg) SystemMsgType() string { return "reasoning" }

// SessionFileFormatVersion is the current version of the session file format.
// Increment when making backward-incompatible changes to the session file structure.
const SessionFileFormatVersion = 1

// SessionMeta is the frontmatter metadata.
type SessionMeta struct {
	CreatedAt      time.Time `config:"created_at"`
	UpdatedAt      time.Time `config:"updated_at"`
	ReasoningLevel int       `config:"reasoning_level"`
	ActiveModel    string    `config:"active_model,omitempty"`
	ContextTokens  int64     `config:"context_tokens,omitempty"`
	Version        int       `config:"version,omitempty"`
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
	// IO — required, provided by the adaptor.
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

	// Feature flags
	DebugAPI      bool
	AutoSummarize bool
	ProxyURL      string

	// External dependencies
	SkillsMgr *skills.Manager

	// Override
	OverrideActiveModel string // If set, overrides the active model (must exist in model config)
}
