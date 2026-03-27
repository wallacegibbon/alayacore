package terminal

// WarningCollector collects warnings during initialization before the TUI is ready.
// This prevents stderr output from corrupting the terminal display.

import (
	"fmt"
	"sync"
)

// Warning represents a single warning message
type Warning struct {
	Message string
}

// WarningCollector buffers warnings until they can be displayed
type WarningCollector struct {
	mu       sync.Mutex
	warnings []Warning
}

// globalWarningCollector is the global instance used during initialization
var globalWarningCollector = &WarningCollector{}

// AddWarning adds a warning to the collector
func AddWarning(format string, args ...interface{}) {
	globalWarningCollector.Add(format, args...)
}

// GetWarnings returns all collected warnings and clears the buffer
func GetWarnings() []Warning {
	return globalWarningCollector.GetAndClear()
}

// Add adds a warning to the collector
func (wc *WarningCollector) Add(format string, args ...interface{}) {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	msg := fmt.Sprintf(format, args...)
	wc.warnings = append(wc.warnings, Warning{Message: msg})
}

// GetAndClear returns all warnings and clears the buffer
func (wc *WarningCollector) GetAndClear() []Warning {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	warnings := wc.warnings
	wc.warnings = nil
	return warnings
}

// HasWarnings checks if there are any warnings
func (wc *WarningCollector) HasWarnings() bool {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return len(wc.warnings) > 0
}
