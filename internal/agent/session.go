package agent

// Session manages conversation state and task execution.
//
// Dependency flow:
//
//	model.conf --(ModelManager)--> available models
//	      ^                               |
//	      |                               v
//	runtime.conf --(RuntimeManager)--> active model name
//	      |                               |
//	      +--------(Session)--------------+
//
// Session is responsible for:
//   - Reading TLV input and turning it into tasks (prompts/commands)
//   - Queueing and running tasks with cancellation support
//   - Streaming model output and system status back over TLV
//   - Delegating model listing/switching to ModelManager + RuntimeManager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alayacore/alayacore/internal/config"
	debugpkg "github.com/alayacore/alayacore/internal/debug"
	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/llm/factory"
	"github.com/alayacore/alayacore/internal/skills"
	"github.com/alayacore/alayacore/internal/stream"
)

// ============================================================================
// Types
// ============================================================================

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
	ActiveModel   string    `config:"active_model"`
	ContextTokens int64     `config:"context_tokens"`
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
	DebugAPI           bool
	AutoSummarize      bool
	NoCompact          bool
	CompactKeepSteps   int
	CompactTruncateLen int
	ProxyURL           string

	// External dependencies
	SkillsMgr *skills.Manager
}

// ============================================================================
// Session Struct
// ============================================================================

// Session manages conversation state and task execution.
type Session struct {
	Messages             []llm.Message
	Agent                *llm.Agent
	Provider             llm.Provider
	SessionFile          string
	CreatedAt            time.Time
	TotalSpent           llm.Usage
	ContextTokens        int64
	ContextLimit         int64
	Input                stream.Input
	Output               stream.Output
	ModelManager         *ModelManager
	RuntimeManager       *RuntimeManager
	SkillsManager        *skills.Manager
	baseTools            []llm.Tool
	systemPrompt         string
	extraSystemPrompt    string
	debugAPI             bool
	autoSummarizeEnabled bool
	compactEnabled       bool
	compactKeepSteps     int
	compactTruncateLen   int
	skillDirs            []string // Skill directories for compaction exemption
	maxSteps             int
	proxyURL             string
	thinkLevel           int
	lastSaveMessages     int  // len(s.Messages) at last successful auto-save; -1 means never saved
	sessionDirty         bool // set when messages change in a way the count doesn't capture (e.g. compaction)

	taskQueue     []QueueItem
	cond          *sync.Cond         // signals when taskQueue becomes non-empty or pausedOnError clears
	sessionCtx    context.Context    // canceled when input is exhausted
	sessionCancel context.CancelFunc // idempotent cancel
	runnerDone    chan struct{}      // closed when taskRunner exits
	inProgress    bool
	pausedOnError bool           // set when a provider/network error occurs; blocks taskRunner until user acts
	taskWg        sync.WaitGroup // tracks in-flight runTask
	cancelCurrent func()
	nextPromptID  uint64
	nextQueueID   uint64
	currentStep   int
	mu            sync.Mutex
}

// WaitDone blocks until the taskRunner has finished processing all queued tasks
// and exited. This should be called after closing the input.
func (s *Session) WaitDone() {
	<-s.runnerDone
}

// ============================================================================
// Session Lifecycle
// ============================================================================

// LoadOrNewSession loads a session from file or creates a new one.
func LoadOrNewSession(cfg SessionConfig) (*Session, string) {
	cfg.SessionFile = expandPath(cfg.SessionFile)
	if cfg.SessionFile != "" {
		if data, err := LoadSession(cfg.SessionFile); err == nil {
			return RestoreFromSession(cfg, data), cfg.SessionFile
		}
	}
	return NewSession(cfg), cfg.SessionFile
}

// NewSession creates a fresh session.
func NewSession(cfg SessionConfig) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		SessionFile:          cfg.SessionFile,
		CreatedAt:            time.Now(),
		Input:                cfg.Input,
		Output:               cfg.Output,
		ModelManager:         NewModelManager(cfg.ModelConfigPath),
		RuntimeManager:       NewRuntimeManager(cfg.RuntimeConfigPath, cfg.ModelConfigPath),
		SkillsManager:        cfg.SkillsMgr,
		baseTools:            cfg.BaseTools,
		systemPrompt:         cfg.SystemPrompt,
		extraSystemPrompt:    cfg.ExtraSystemPrompt,
		debugAPI:             cfg.DebugAPI,
		autoSummarizeEnabled: cfg.AutoSummarize,
		compactEnabled:       !cfg.NoCompact,
		compactKeepSteps:     cfg.CompactKeepSteps,
		compactTruncateLen:   cfg.CompactTruncateLen,
		skillDirs:            buildSkillDirSet(cfg.SkillsMgr),
		proxyURL:             cfg.ProxyURL,
		maxSteps:             cfg.MaxSteps,
		thinkLevel:           config.DefaultThinkLevel,
		lastSaveMessages:     -1,
		taskQueue:            make([]QueueItem, 0),
		sessionCtx:           sessionCtx,
		sessionCancel:        sessionCancel,
		runnerDone:           make(chan struct{}),
	}
	s.initModelManager()
	s.cond = sync.NewCond(&s.mu)
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()
	return s
}

// RestoreFromSession creates a session from saved data.
func RestoreFromSession(cfg SessionConfig, data *SessionData) *Session {
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s := &Session{
		Messages:             data.Messages,
		SessionFile:          cfg.SessionFile,
		CreatedAt:            data.CreatedAt,
		Input:                cfg.Input,
		Output:               cfg.Output,
		ModelManager:         NewModelManager(cfg.ModelConfigPath),
		RuntimeManager:       NewRuntimeManager(cfg.RuntimeConfigPath, cfg.ModelConfigPath),
		SkillsManager:        cfg.SkillsMgr,
		baseTools:            cfg.BaseTools,
		systemPrompt:         cfg.SystemPrompt,
		extraSystemPrompt:    cfg.ExtraSystemPrompt,
		debugAPI:             cfg.DebugAPI,
		autoSummarizeEnabled: cfg.AutoSummarize,
		compactEnabled:       !cfg.NoCompact,
		compactKeepSteps:     cfg.CompactKeepSteps,
		compactTruncateLen:   cfg.CompactTruncateLen,
		skillDirs:            buildSkillDirSet(cfg.SkillsMgr),
		proxyURL:             cfg.ProxyURL,
		maxSteps:             cfg.MaxSteps,
		thinkLevel:           data.ThinkLevel,
		ContextTokens:        data.ContextTokens,
		lastSaveMessages:     len(data.Messages),
		taskQueue:            make([]QueueItem, 0),
		sessionCtx:           sessionCtx,
		sessionCancel:        sessionCancel,
		runnerDone:           make(chan struct{}),
	}
	s.initModelManager()

	// Override runtime config default with the model saved in the session file.
	// If the model was removed from config since the session was saved,
	// fall back to whatever initModelManager already set.
	if data.ActiveModel != "" {
		_ = s.ModelManager.SetActiveByName(data.ActiveModel) //nolint:errcheck // best-effort restore, fall back to initModelManager default
	}

	// Apply context limit from the resolved model so the status bar
	// can show "tokens/limit (pct%)" immediately, before any API call.
	if model := s.ModelManager.GetActive(); model != nil {
		s.applyModelContextLimit(model)
	}

	s.cond = sync.NewCond(&s.mu)
	s.sendSystemInfo()
	go s.readFromInput()
	go s.taskRunner()

	// Send TLV chunks directly to output (avoids reconstruction)
	for _, chunk := range data.TLVChunks {
		//nolint:errcheck // Best effort write, errors ignored
		_ = stream.WriteTLV(s.Output, chunk.Tag, chunk.Value)
	}
	if len(data.TLVChunks) > 0 {
		s.Output.Flush()
	}
	return s
}

// ============================================================================
// Model Management
// ============================================================================

// SwitchModel switches the session to use a new model.
//
// DEADLOCK GOTCHA: Don't hold mutex while calling methods that may need the same mutex.
// Pattern: lock → update fields → unlock → call methods.
func (s *Session) SwitchModel(modelConfig *ModelConfig) error {
	if err := s.initAgentFromConfig(modelConfig); err != nil {
		return err
	}
	s.applyModelContextLimit(modelConfig)
	s.sendSystemInfo()
	return nil
}

func (s *Session) initModelManager() {
	if s.ModelManager == nil || s.RuntimeManager == nil {
		return
	}

	activeModelName := s.RuntimeManager.GetActiveModel()
	if activeModelName != "" {
		if err := s.ModelManager.SetActiveByName(activeModelName); err == nil {
			return
		}
	}
	s.ModelManager.SetActiveToFirst()
}

// activeModelName returns the display name of the currently active model.
func (s *Session) activeModelName() string {
	if s.ModelManager == nil {
		return ""
	}
	if model := s.ModelManager.GetActive(); model != nil {
		return model.Name
	}
	return ""
}

// GetRuntimeManager returns the runtime manager for the session
func (s *Session) GetRuntimeManager() *RuntimeManager {
	return s.RuntimeManager
}

func (s *Session) ensureAgentInitialized() string {
	s.mu.Lock()
	if s.Agent != nil && s.Provider != nil {
		s.mu.Unlock()
		return ""
	}
	s.mu.Unlock()

	if s.ModelManager == nil {
		return "Model manager not initialized"
	}

	activeModel := s.ModelManager.GetActive()
	if activeModel == nil {
		return "No model configured. Please add a model to ~/.alayacore/model.conf"
	}

	provider, err := createProviderFromConfig(activeModel, s.debugAPI, s.proxyURL)
	if err != nil {
		return "Failed to create provider: " + err.Error()
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider:          provider,
		Tools:             s.baseTools,
		SystemPrompt:      s.systemPrompt,
		ExtraSystemPrompt: s.extraSystemPrompt,
		MaxSteps:          s.maxSteps,
	})

	s.mu.Lock()
	s.Agent = agent
	s.Provider = provider
	s.mu.Unlock()

	s.applyModelContextLimit(activeModel)
	s.syncThinkToProvider()
	return ""
}

func (s *Session) initAgentFromConfig(modelConfig *ModelConfig) error {
	provider, err := createProviderFromConfig(modelConfig, s.debugAPI, s.proxyURL)
	if err != nil {
		return err
	}

	agent := llm.NewAgent(llm.AgentConfig{
		Provider:          provider,
		Tools:             s.baseTools,
		SystemPrompt:      s.systemPrompt,
		ExtraSystemPrompt: s.extraSystemPrompt,
		MaxSteps:          s.maxSteps,
	})

	s.mu.Lock()
	s.Agent = agent
	s.Provider = provider
	s.mu.Unlock()

	s.syncThinkToProvider()
	return nil
}

func (s *Session) applyModelContextLimit(model *ModelConfig) {
	if model == nil || model.ContextLimit <= 0 {
		return
	}
	s.mu.Lock()
	s.ContextLimit = int64(model.ContextLimit)
	s.mu.Unlock()
}

// syncThinkToProvider propagates the session's thinking level to the
// current provider. Must be called after Provider is set.
func (s *Session) syncThinkToProvider() {
	if s.Provider != nil {
		s.Provider.SetReasoningLevel(s.thinkLevel)
	}
}

// SetThinkLevel sets the think level.
// See config.ThinkLevelOff, config.ThinkLevelNormal, config.ThinkLevelMax.
func (s *Session) SetThinkLevel(level int) {
	s.mu.Lock()
	s.thinkLevel = level
	s.mu.Unlock()

	s.syncThinkToProvider()

	s.sendSystemInfo()
}

func createProviderFromConfig(config *ModelConfig, debugAPI bool, proxyURL string) (llm.Provider, error) {
	var client *http.Client
	var err error
	if proxyURL != "" {
		if debugAPI {
			client, err = debugpkg.NewHTTPClientWithProxyAndDebug(proxyURL)
		} else {
			client, err = debugpkg.NewHTTPClientWithProxy(proxyURL)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP client with proxy: %w", err)
		}
	} else if debugAPI {
		client = debugpkg.NewHTTPClient()
	}

	return factory.NewProvider(factory.ProviderConfig{
		Type:        config.ProtocolType,
		APIKey:      config.APIKey,
		BaseURL:     config.BaseURL,
		Model:       config.ModelName,
		HTTPClient:  client,
		PromptCache: config.PromptCache,
		MaxTokens:   config.MaxTokens,
	})
}

// ============================================================================
// Input Processing
// ============================================================================

// isCommandImmediate returns true if the command should be handled immediately
// without queuing. Immediate commands are those that control task execution
// (cancel, continue) or query/modify session state (model_load, taskqueue operations).
func isCommandImmediate(cmd string) bool {
	// Extract the command name (first word) for commands that accept arguments.
	name := cmd
	if idx := strings.IndexByte(cmd, ' '); idx >= 0 {
		name = cmd[:idx]
	}
	switch name {
	case commandNameCancel, commandNameCancelAll, commandNameModelLoad, commandNameTaskQueueGetAll, commandNameThink:
		return true
	}
	return strings.HasPrefix(cmd, commandNameTaskQueueDel+" ") || strings.HasPrefix(cmd, commandNameModelSet+" ")
}

func (s *Session) readFromInput() {
	defer func() {
		s.sessionCancel()
		s.cond.Signal()
	}()
	for {
		tag, value, err := stream.ReadTLV(s.Input)
		if err != nil {
			return
		}
		if tag != stream.TagTextUser {
			s.writeError(domainerrors.NewSessionErrorf("input", "Invalid input tag: %s", tag).Error())
			continue
		}
		if len(value) > 0 && value[0] == ':' {
			cmd := value[1:]
			if isCommandImmediate(cmd) {
				s.handleCommand(context.Background(), cmd)
			} else {
				s.submitDeferredCommand(cmd)
			}
		} else {
			s.submitTask(UserPrompt{Text: value})
		}
	}
}

// ============================================================================
// Prompt Processing
// ============================================================================

func (s *Session) handleUserPrompt(ctx context.Context, prompt string) {
	if s.shouldAutoSummarize() {
		s.doAutoSummarize(ctx)
	}

	s.Messages = append(s.Messages, llm.NewUserMessage(prompt))

	_, err := s.processPrompt(ctx, s.Messages)

	s.Messages = cleanIncompleteToolCalls(s.Messages)

	if err != nil {
		s.writeError(err.Error())
		s.mu.Lock()
		s.pausedOnError = true
		s.mu.Unlock()
		s.sendSystemInfo()
		return
	}

	s.compactHistory()
}

func (s *Session) shouldAutoSummarize() bool {
	return s.autoSummarizeEnabled && s.ContextLimit > 0 && s.ContextTokens > 0 &&
		s.ContextTokens >= s.ContextLimit*65/100
}

func (s *Session) doAutoSummarize(ctx context.Context) {
	usage := float64(s.ContextTokens) * 100 / float64(s.ContextLimit)
	s.writeNotifyf("Context usage at %d/%d tokens (%.0f%%). Auto-summarizing...",
		s.ContextTokens, s.ContextLimit, usage)
	s.summarize(ctx)
}

func (s *Session) processPrompt(ctx context.Context, history []llm.Message) (int64, error) {
	promptID := atomic.AddUint64(&s.nextPromptID, 1) - 1

	var stepCount int
	var outputTokens int64

	assembleID := func(id string) string {
		return stream.NewStreamID(promptID, stepCount, id)
	}

	_, err := s.Agent.Stream(ctx, history, llm.StreamCallbacks{
		OnTextDelta: func(delta string) error {
			//nolint:errcheck // Best effort write, errors ignored
			_ = stream.WriteTLV(s.Output, stream.TagTextAssistant, stream.WrapDelta(assembleID(stream.SuffixText), delta))
			s.Output.Flush()
			return nil
		},
		OnReasoningDelta: func(delta string) error {
			//nolint:errcheck // Best effort write, errors ignored
			_ = stream.WriteTLV(s.Output, stream.TagTextReasoning, stream.WrapDelta(assembleID(stream.SuffixReasoning), delta))
			s.Output.Flush()
			return nil
		},
		OnToolCall: func(toolCallID, toolName string, input json.RawMessage) error {
			s.writeToolCall(toolName, string(input), toolCallID)
			s.Output.Flush()
			return nil
		},
		OnToolResult: func(toolCallID string, output llm.ToolResultOutput) error {
			status := "success"
			if textOutput, ok := output.(llm.ToolResultOutputText); ok {
				s.writeToolOutput(toolCallID, textOutput.Text)
			} else if errOutput, ok := output.(llm.ToolResultOutputError); ok {
				status = "error"
				s.writeToolOutput(toolCallID, errOutput.Error)
			}
			s.writeToolResult(toolCallID, status)
			return nil
		},
		OnStepStart: func(step int) error {
			stepCount = step
			s.mu.Lock()
			s.currentStep = step
			s.mu.Unlock()
			s.sendSystemInfo()
			return nil
		},
		OnStepFinish: func(messages []llm.Message, usage llm.Usage) error {
			s.trackUsage(usage)
			if len(messages) > 0 {
				s.Messages = append(s.Messages, messages...)
			}
			outputTokens += usage.OutputTokens
			s.autoSaveIfEnabled()
			return nil
		},
	})

	s.Output.Flush()

	if err != nil {
		return 0, err
	}

	return outputTokens, nil
}

// ============================================================================
// Path Helpers
// ============================================================================

// buildSkillDirSet creates a slice of absolute skill directory paths.
// Called once at session creation since skills are fixed during process lifetime.
func buildSkillDirSet(skillsMgr *skills.Manager) []string {
	if skillsMgr == nil {
		return nil
	}
	return skillsMgr.GetSkillDirs()
}

// expandPath expands ~ to the user's home directory.
// See config.ExpandPath for the exported version.
func expandPath(path string) string {
	return config.ExpandPath(path)
}
