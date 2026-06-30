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

	agentpkg "github.com/alayacore/alayacore/internal/agent"
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

	// MCP auth status — tracks the currently authorizing server during OAuth flow.
	mcpAuthServer     string
	mcpAuthInProgress bool
	mcpAuthJustDone   bool // set to true on mcp_auth:done/error, consumed by takeMCPAuthDone

	// Model fields
	models          []agentpkg.ModelConfig
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

	// Pending MCP auth confirms — set by waitMCPInit (or handleSystemMsg),
	// consumed by the Terminal tick handler to open the confirm overlay
	// for OAuth authorization. Each entry is a server needing auth.
	pendingMCPAuth []mcpAuthPending
}

// toolConfirmPending holds a single pending tool confirmation.
type toolConfirmPending struct {
	ID    string
	Name  string
	Input string
}

// mcpAuthPending holds a pending MCP OAuth authorization request.
type mcpAuthPending struct {
	ServerName string
	ServerURL  string
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
func (s *sessionState) updateModelList(models []agentpkg.ModelConfig) {
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

// updateMCPAuth atomically updates the MCP OAuth authorization status.
// Called when the session sends an mcp_auth system message.
func (s *sessionState) updateMCPAuth(server, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch status {
	case "in_progress":
		s.mcpAuthServer = server
		s.mcpAuthInProgress = true
		s.mcpAuthJustDone = false
	case "done", "error":
		s.mcpAuthServer = ""
		s.mcpAuthInProgress = false
		s.mcpAuthJustDone = true
	}
}

// takeMCPAuthDone returns whether an MCP authorization just completed
// (mcp_auth:done or mcp_auth:error was received) since the last call.
// This is a one-shot flag — it's reset to false after reading.
func (s *sessionState) takeMCPAuthDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.mcpAuthJustDone
	s.mcpAuthJustDone = false
	return v
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

// setMCPAuthPending appends a pending MCP OAuth authorization request.
func (s *sessionState) setMCPAuthPending(serverName, serverURL string) {
	s.mu.Lock()
	s.pendingMCPAuth = append(s.pendingMCPAuth, mcpAuthPending{
		ServerName: serverName, ServerURL: serverURL,
	})
	s.mu.Unlock()
}

// takeMCPAuthPending pops the next pending MCP OAuth authorization request.
// Returns (serverName, serverURL, ok). If no pending requests, ok is false.
func (s *sessionState) takeMCPAuthPending() (serverName, serverURL string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingMCPAuth) == 0 {
		return "", "", false
	}
	p := s.pendingMCPAuth[0]
	s.pendingMCPAuth = s.pendingMCPAuth[1:]
	return p.ServerName, p.ServerURL, true
}

// snapshotStatus returns a consistent point-in-time view of session status.
func (s *sessionState) snapshotStatus() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusSnapshot{
		ContextTokens:     s.contextTokens,
		ContextLimit:      s.contextLimit,
		InProgress:        s.inProgress,
		CurrentStep:       s.currentStep,
		MaxSteps:          s.maxSteps,
		LastCurrentStep:   s.lastCurrentStep,
		LastMaxSteps:      s.lastMaxSteps,
		TaskError:         s.lastTaskError,
		ReasoningLevel:    s.reasoningLevel,
		ActiveTheme:       s.activeTheme,
		ActiveThemeData:   s.activeThemeData,
		VideoFPS:          s.videoFPS,
		VideoRes:          s.videoRes,
		MCPInitStatus:     s.mcpInitStatus,
		MCPAuthServer:     s.mcpAuthServer,
		MCPAuthInProgress: s.mcpAuthInProgress,
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
