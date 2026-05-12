package agent

// Type definitions for the session package.
// Kept separate for readability — no logic, just data structures.

import (
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/stream"
)

// Task represents a unit of work for the session.
type Task interface {
	isTask()
	GetQueueID() string
}

// QueueItem wraps a Task with metadata for queue management
type QueueItem struct {
	Task
	QueueID   string
	CreatedAt time.Time
}

// UserPrompt is a user text input task
type UserPrompt struct {
	Text    string
	queueID string
}

func (UserPrompt) isTask() {}

func (u UserPrompt) GetQueueID() string { return u.queueID }

// CommandPrompt is a command task
type CommandPrompt struct {
	Command string
	queueID string
}

func (CommandPrompt) isTask() {}

func (c CommandPrompt) GetQueueID() string { return c.queueID }

// QueueItemInfo holds serializable queue item data for clients.
type QueueItemInfo struct {
	QueueID   string `json:"queue_id"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// SystemInfo holds session state for clients.
type SystemInfo struct {
	ContextTokens     int64           `json:"context"`
	ContextLimit      int64           `json:"context_limit"`
	TotalTokens       int64           `json:"total"`
	QueueItems        []QueueItemInfo `json:"queue_items,omitempty"`
	InProgress        bool            `json:"in_progress"`
	CurrentStep       int             `json:"current_step,omitempty"`
	MaxSteps          int             `json:"max_steps,omitempty"`
	TaskError         bool            `json:"task_error,omitempty"`
	Models            []ModelInfo     `json:"models,omitempty"`
	ActiveModelID     int             `json:"active_model_id,omitempty"`
	ActiveModelConfig *ModelConfig    `json:"active_model_config,omitempty"`
	ActiveModelName   string          `json:"active_model_name,omitempty"`
	HasModels         bool            `json:"has_models"`
	ModelConfigPath   string          `json:"model_config_path,omitempty"`
	ThinkLevel        int             `json:"think_level"`
}

// SessionMeta is the frontmatter metadata.
type SessionMeta struct {
	CreatedAt     time.Time `config:"created_at"`
	UpdatedAt     time.Time `config:"updated_at"`
	ThinkLevel    int       `config:"think_level"`
	ActiveModel   string    `config:"active_model,omitempty"`
	ContextTokens int64     `config:"context_tokens,omitempty"`
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
	Input  stream.Input
	Output stream.Output

	// Files — paths to configuration and session files. Empty means default / none.
	SessionFile       string
	ModelConfigPath   string
	RuntimeConfigPath string

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
