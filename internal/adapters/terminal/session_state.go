package terminal

// Session state cache: status and models written by the session
// goroutine and read by the Bubble Tea goroutine for display updates.
//
// All access is protected by the embedded sync.Mutex. The two goroutines
// never hold this lock simultaneously with WindowBuffer.mu — snapshot
// methods and system-tag updates are exclusive paths. See output.go for
// the full lock ordering design.

import (
	"sync"

	"github.com/alayacore/alayacore/internal/config"
	"github.com/alayacore/alayacore/internal/theme"
)

// sessionState caches the session's status, model, and queue item state
// for race-free access between the session and Bubble Tea goroutines.
//
// mu is a pointer to prevent copying when sessionState is embedded in
// outputWriter by value.
type sessionState struct {
	mu *sync.Mutex

	// Status fields
	contextTokens   int64
	contextLimit    int64
	inProgress      bool
	currentStep     int
	maxSteps        int
	lastCurrentStep int
	lastMaxSteps    int
	lastTaskError   bool
	reasoningLevel  int

	// Video config
	videoFPS int
	videoRes int

	// MCP init status — tracks the initialization phase.
	// Values: "" (no MCP), "starting", "ready", "auth_required".
	mcpInitStatus string

	// Pending MCP auth confirm — set when the session sends mcp_auth:confirm,
	// consumed by the Terminal tick handler to open the confirm dialog.
	// Only one server is prompted at a time (session serializes the list).
	mcpAuthPendingName string
	mcpAuthPendingURL  string

	// Model fields
	models          []config.ModelConfig
	activeModelID   int
	activeModelName string

	// Theme — active theme broadcast by the session via TagSystemMsg.
	// The terminal reads this in updateStatus() and applies the theme visually
	// when it detects a change from the previously applied theme.
	activeTheme     string
	activeThemeData *theme.Theme
	cachedThemeList []ThemeEntry

	// Pending tool confirms — set by handleSystemToolConfirm, consumed by
	// the Terminal tick handler to open the confirm overlay.
	// Stored as a queue so multiple confirms arriving at once aren't lost.
	pendingToolConfirms []toolConfirmPending
}

// toolConfirmPending holds a single pending tool confirmation.
type toolConfirmPending struct {
	ID    string
	Name  string
	Input string
}

// updateTask atomically updates task progress fields.
func (s *sessionState) updateTask(inProgress bool, currentStep, maxSteps int, context int64, taskError bool) {
	s.mu.Lock()
	// Save step info when task completes (transition from in-progress to done)
	if s.inProgress && !inProgress && s.maxSteps > 0 {
		s.lastCurrentStep = s.currentStep
		s.lastMaxSteps = s.maxSteps
		s.lastTaskError = taskError
	}
	// Reset last step info when new task starts (transition from not-in-progress to in-progress)
	if !s.inProgress && inProgress {
		s.lastCurrentStep = 0
		s.lastMaxSteps = 0
		s.lastTaskError = false
	}
	s.inProgress = inProgress
	s.currentStep = currentStep
	s.maxSteps = maxSteps
	s.contextTokens = context
	s.mu.Unlock()
}

// updateModel atomically updates active model info.
func (s *sessionState) updateModel(activeID int, activeName string, contextLimit int64) {
	s.mu.Lock()
	s.activeModelID = activeID
	s.activeModelName = activeName
	s.contextLimit = contextLimit
	s.mu.Unlock()
}

// updateModelList atomically replaces the full model list.
func (s *sessionState) updateModelList(models []config.ModelConfig) {
	s.mu.Lock()
	s.models = models
	// Also sync active name if models list provides it
	for _, m := range models {
		if m.ID == s.activeModelID {
			s.activeModelName = m.Name
			break
		}
	}
	s.mu.Unlock()
}

// updateTheme atomically updates the active theme.
// When themeData is nil (theme change with just name), the cached
// theme list is used to look up the full data.
func (s *sessionState) updateTheme(name string, themeData *theme.Theme) {
	s.mu.Lock()
	s.activeTheme = name
	if themeData != nil {
		s.activeThemeData = themeData
	} else {
		// Look up from cached list
		for _, ti := range s.cachedThemeList {
			if ti.Name == name {
				s.activeThemeData = ti.Theme
				break
			}
		}
	}
	s.mu.Unlock()
}

// updateThemeList atomically replaces the cached theme list.
func (s *sessionState) updateThemeList(themes []ThemeEntry) {
	s.mu.Lock()
	s.cachedThemeList = themes
	s.mu.Unlock()
}

// updateReasoning atomically updates the reasoning level.
func (s *sessionState) updateReasoning(level int) {
	s.mu.Lock()
	s.reasoningLevel = level
	s.mu.Unlock()
}

// updateVideoConfig atomically updates the video FPS and resolution.
func (s *sessionState) updateVideoConfig(fps, res int) {
	s.mu.Lock()
	s.videoFPS = fps
	s.videoRes = res
	s.mu.Unlock()
}

// updateMCPInitStatus atomically updates the MCP initialization phase.
// Called when the session sends an mcp_init system message.
func (s *sessionState) updateMCPInitStatus(status string) {
	s.mu.Lock()
	s.mcpInitStatus = status
	s.mu.Unlock()
}

// setMCPAuthPending stores a pending MCP auth confirmation request.
// Consumed by takeMCPAuthPending in the Terminal tick handler.
func (s *sessionState) setMCPAuthPending(server, url string) {
	s.mu.Lock()
	s.mcpAuthPendingName = server
	s.mcpAuthPendingURL = url
	s.mu.Unlock()
}

// takeMCPAuthPending pops the pending MCP auth confirmation.
// Returns (server, url, ok). Only one pending at a time.
func (s *sessionState) takeMCPAuthPending() (server, url string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcpAuthPendingName == "" {
		return "", "", false
	}
	server = s.mcpAuthPendingName
	url = s.mcpAuthPendingURL
	s.mcpAuthPendingName = ""
	s.mcpAuthPendingURL = ""
	return server, url, true
}

// setToolConfirmPending appends a pending tool confirmation request.
func (s *sessionState) setToolConfirmPending(id, toolName, toolInput string) {
	s.mu.Lock()
	s.pendingToolConfirms = append(s.pendingToolConfirms, toolConfirmPending{
		ID: id, Name: toolName, Input: toolInput,
	})
	s.mu.Unlock()
}

// takeToolConfirmPending pops the next pending tool confirmation.
// Returns (id, toolName, toolInput, ok). If no pending confirms, ok is false.
func (s *sessionState) takeToolConfirmPending() (id, toolName, toolInput string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingToolConfirms) == 0 {
		return "", "", "", false
	}
	p := s.pendingToolConfirms[0]
	s.pendingToolConfirms = s.pendingToolConfirms[1:]
	return p.ID, p.Name, p.Input, true
}

// snapshotStatus returns a consistent point-in-time view of session status.
func (s *sessionState) snapshotStatus() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusSnapshot{
		ContextTokens:   s.contextTokens,
		ContextLimit:    s.contextLimit,
		InProgress:      s.inProgress,
		CurrentStep:     s.currentStep,
		MaxSteps:        s.maxSteps,
		LastCurrentStep: s.lastCurrentStep,
		LastMaxSteps:    s.lastMaxSteps,
		TaskError:       s.lastTaskError,
		ReasoningLevel:  s.reasoningLevel,
		ActiveTheme:     s.activeTheme,
		ActiveThemeData: s.activeThemeData,
		VideoFPS:        s.videoFPS,
		VideoRes:        s.videoRes,
		MCPInitStatus:   s.mcpInitStatus,
	}
}

// snapshotModels returns a consistent point-in-time view of model state.
func (s *sessionState) snapshotModels() ModelSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := ModelSnapshot{
		ActiveID:   s.activeModelID,
		ActiveName: s.activeModelName,
	}
	if s.models != nil {
		snap.Models = s.models
	}
	return snap
}
