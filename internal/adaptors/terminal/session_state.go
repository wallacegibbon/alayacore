package terminal

// Session state cache: status, models, and queue items written by the session
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
type sessionState struct {
	mu sync.Mutex

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

	// Model fields
	models          []agentpkg.ModelInfo
	activeModelID   int
	activeModelName string
	modelConfigPath string

	// Queue items — set by handleSystemTask, cleared by GetQueueItems
	pendingQueueItems []QueueItem

	// Theme — active theme broadcast by the session via TagSystemMsg.
	// The terminal reads this in updateStatus() and applies the theme visually
	// when it detects a change from the previously applied theme.
	activeTheme     string
	activeThemeData *theme.Theme
	cachedThemeList []ThemeEntry
}

// updateTask atomically updates task progress fields and queue items.
func (s *sessionState) updateTask(inProgress bool, currentStep, maxSteps int, context int64, taskError bool, queueItems []QueueItem) {
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
	s.pendingQueueItems = queueItems
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
func (s *sessionState) updateModelList(models []agentpkg.ModelInfo, configPath string) {
	s.mu.Lock()
	s.models = models
	s.modelConfigPath = configPath
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

// snapshotStatus returns a consistent point-in-time view of session status.
func (s *sessionState) snapshotStatus() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusSnapshot{
		ContextTokens:   s.contextTokens,
		ContextLimit:    s.contextLimit,
		QueueCount:      len(s.pendingQueueItems),
		InProgress:      s.inProgress,
		CurrentStep:     s.currentStep,
		MaxSteps:        s.maxSteps,
		LastCurrentStep: s.lastCurrentStep,
		LastMaxSteps:    s.lastMaxSteps,
		TaskError:       s.lastTaskError,
		ReasoningLevel:  s.reasoningLevel,
		ActiveTheme:     s.activeTheme,
		ActiveThemeData: s.activeThemeData,
	}
}

// snapshotModels returns a consistent point-in-time view of model state.
func (s *sessionState) snapshotModels() ModelSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := ModelSnapshot{
		ActiveID:   s.activeModelID,
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
