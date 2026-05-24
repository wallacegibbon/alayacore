package terminal

// WarningCollector collects warnings during initialization before the TUI is ready.
// This prevents stderr output from corrupting the terminal display.
//
// WarningCollector is NOT safe for concurrent use. It is only used during
// single-threaded program initialization (before any goroutines are running).

import (
	"fmt"
)

// Warning represents a single warning message
type Warning struct {
	Message string
}

// WarningCollector buffers warnings until they can be displayed
type WarningCollector struct {
	warnings []Warning
}

// Addf adds a warning to the collector
func (wc *WarningCollector) Addf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	wc.warnings = append(wc.warnings, Warning{Message: msg})
}

// GetAndClear returns all warnings and clears the buffer
func (wc *WarningCollector) GetAndClear() []Warning {
	warnings := wc.warnings
	wc.warnings = nil
	return warnings
}

// AddWarningf adds a warning to the collector (nil-safe)
func AddWarningf(wc *WarningCollector, format string, args ...any) {
	if wc != nil {
		wc.Addf(format, args...)
	}
}

// HasWarnings checks if there are any warnings
func (wc *WarningCollector) HasWarnings() bool {
	return len(wc.warnings) > 0
}
