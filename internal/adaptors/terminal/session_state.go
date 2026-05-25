package terminal

// Session state cache: status, models, and queue items written by the session
// goroutine and read by the Bubble Tea goroutine for display updates.
//
// All access is protected by the embedded sync.Mutex so the two goroutines
// never race. The locking order is:
//
//	outputWriter.mu → sessionState.mu    (session goroutine in handleSystemTag)
//	WindowBuffer.mu  → sessionState.mu    (Bubble Tea goroutine in snapshots)
//
// See output.go for the full lock ordering design.

import (
	"sync"
	"time"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
)

// sessionState caches the session's status, model, and queue item state
// for race-free access between the session and Bubble Tea goroutines.
type sessionState struct {
	mu sync.Mutex

	// Status fields
	contextTokens   int64
	contextLimit    int64
	queueCount      int
	inProgress      bool
	currentStep     int
	maxSteps        int
	lastCurrentStep int
	lastMaxSteps    int
	lastTaskError   bool
	reasoningLevel  int

	// Model fields
	models          []agentpkg.ModelInfo
	activeModelID   int
	activeModelName string
	hasModels       bool
	modelConfigPath string

	// Queue items — set by handleSystemTag, cleared by GetQueueItems
	pendingQueueItems []QueueItem

	// Theme
	activeTheme string
}

// updateFromSystemInfo atomically updates all fields from a SystemInfo message.
func (s *sessionState) updateFromSystemInfo(info agentpkg.SystemInfo) {
	s.mu.Lock()
	s.updateStepTracking(info)
	s.updateModelState(info)
	s.updateQueueItems(info)
	s.currentStep = info.CurrentStep
	s.maxSteps = info.MaxSteps
	s.reasoningLevel = info.ReasoningLevel
	s.activeTheme = info.ActiveTheme
	s.mu.Unlock()
}

// updateStepTracking handles the step-info bookkeeping around task transitions.
// Caller must hold s.mu.
func (s *sessionState) updateStepTracking(info agentpkg.SystemInfo) {
	// Save step info when task completes (transition from in-progress to done)
	if s.inProgress && !info.InProgress && s.maxSteps > 0 {
		s.lastCurrentStep = s.currentStep
		s.lastMaxSteps = s.maxSteps
		s.lastTaskError = info.TaskError
	}
	// Reset last step info when new task starts (transition from not-in-progress to in-progress)
	if !s.inProgress && info.InProgress {
		s.lastCurrentStep = 0
		s.lastMaxSteps = 0
		s.lastTaskError = false
	}
	s.inProgress = info.InProgress
	s.queueCount = len(info.QueueItems)
	s.contextTokens = info.ContextTokens
	s.contextLimit = info.ContextLimit
}

// updateModelState updates cached model-related fields from SystemInfo.
// Caller must hold s.mu.
func (s *sessionState) updateModelState(info agentpkg.SystemInfo) {
	s.models = info.Models
	s.activeModelID = info.ActiveModelID
	s.hasModels = info.HasModels
	s.modelConfigPath = info.ModelConfigPath
	s.activeModelName = info.ActiveModelName
}

// updateQueueItems converts and stores the queue items from SystemInfo.
// Caller must hold s.mu.
func (s *sessionState) updateQueueItems(info agentpkg.SystemInfo) {
	items := make([]QueueItem, len(info.QueueItems))
	for i, item := range info.QueueItems {
		createdAt, err := time.Parse(time.RFC3339, item.CreatedAt)
		if err != nil {
			createdAt = time.Now()
		}
		items[i] = QueueItem{
			QueueID:   item.QueueID,
			Type:      item.Type,
			Content:   item.Content,
			CreatedAt: createdAt,
		}
	}
	s.pendingQueueItems = items
}

// snapshotStatus returns a consistent point-in-time view of session status.
func (s *sessionState) snapshotStatus() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusSnapshot{
		ContextTokens:   s.contextTokens,
		ContextLimit:    s.contextLimit,
		QueueCount:      s.queueCount,
		InProgress:      s.inProgress,
		CurrentStep:     s.currentStep,
		MaxSteps:        s.maxSteps,
		LastCurrentStep: s.lastCurrentStep,
		LastMaxSteps:    s.lastMaxSteps,
		TaskError:       s.lastTaskError,
		ReasoningLevel:  s.reasoningLevel,
		ActiveTheme:     s.activeTheme,
	}
}

// snapshotModels returns a consistent point-in-time view of model state.
func (s *sessionState) snapshotModels() ModelSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := ModelSnapshot{
		ActiveID:   s.activeModelID,
		HasModels:  s.hasModels,
		ConfigPath: s.modelConfigPath,
		ActiveName: s.activeModelName,
	}
	if s.models != nil {
		snap.Models = s.models
	}
	return snap
}

// takeQueueItems returns and clears the pending queue items.
func (s *sessionState) takeQueueItems() []QueueItem {
	s.mu.Lock()
	items := s.pendingQueueItems
	s.pendingQueueItems = nil
	s.mu.Unlock()
	return items
}
