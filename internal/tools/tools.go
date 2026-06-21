// Package tools provides agent-accessible tools for file operations,
// command execution, and content search. Each tool is a self-contained
// module with its own input type, schema, and execution function.
//
// Tool registration: new built-in tools should be added to the
// BuiltinTools registry below. This keeps tool discovery centralized
// so callers (app.Setup) don't need to know about each tool individually.
package tools

import "github.com/alayacore/alayacore/internal/llm"

// ToolRegistration describes a built-in tool for automatic discovery.
// Add new tools to BuiltinTools rather than wiring them manually.
type ToolRegistration struct {
	// New creates the tool instance. Called only when the tool is available.
	New func() llm.Tool

	// Available returns true if the tool can be used on this system.
	// If nil, the tool is always available.
	Available func() bool
}

// BuiltinTools is the registry of all built-in tools.
// Tools with an Available check that returns false are skipped.
// To add a new built-in tool, append an entry here.
var BuiltinTools = []ToolRegistration{
	{New: NewReadFileTool},
	{New: NewEditFileTool},
	{New: NewWriteFileTool},
	{New: NewExecuteCommandTool},
	{New: NewSearchContentTool, Available: RGAvailable},
}

// DefaultTools returns llm.Tool instances for all available built-in tools.
// Tools whose Available check returns false are omitted.
func DefaultTools() []llm.Tool {
	var result []llm.Tool
	for _, reg := range BuiltinTools {
		if reg.Available != nil && !reg.Available() {
			continue
		}
		result = append(result, reg.New())
	}
	return result
}
