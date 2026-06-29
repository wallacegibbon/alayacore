// Package tools provides agent-accessible tools for file operations,
// command execution, and content search. Each tool is a self-contained
// module with its own input type, schema, and execution function.
//
// Tool registration: new built-in tools should be added to the
// BuiltinTools registry below. This keeps tool discovery centralized
// so callers (app.Setup) don't need to know about each tool individually.
package tools

import (
	"fmt"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

// ToolRegistration describes a built-in tool for automatic discovery.
// Add new tools to BuiltinTools rather than wiring them manually.
type ToolRegistration struct {
	// New creates the tool instance.
	New func() llm.Tool

	// Name is the tool name used for filtering via --builtin-tools.
	Name string
}

// BuiltinTools is the registry of all built-in tools.
// To add a new built-in tool, append an entry here.
var BuiltinTools = []ToolRegistration{
	{New: NewReadFileTool, Name: "read_file"},
	{New: NewEditFileTool, Name: "edit_file"},
	{New: NewWriteFileTool, Name: "write_file"},
	{New: NewExecuteCommandTool, Name: "execute_command"},
	{New: NewSearchContentTool, Name: "search_content"},
}

// DefaultTools returns llm.Tool instances for built-in tools based on the
// provided filter:
//   - filter is nil → all built-in tools are returned.
//   - filter is empty → no built-in tools are returned (user relies on MCP).
//   - filter has items → only tools whose names appear in filter are returned.
//
// Returns an error if any name in filter does not match a registered built-in tool.
func DefaultTools(filter []string) ([]llm.Tool, error) {
	// nil means "not specified" → enable all tools.
	if filter == nil {
		var result = make([]llm.Tool, 0, len(BuiltinTools))
		for _, reg := range BuiltinTools {
			result = append(result, reg.New())
		}
		return result, nil
	}

	// Empty slice means explicitly disabled → no built-in tools.
	if len(filter) == 0 {
		return nil, nil
	}

	// Build a set of valid tool names.
	valid := make(map[string]bool, len(BuiltinTools))
	for _, reg := range BuiltinTools {
		valid[reg.Name] = true
	}

	// Check for unknown tool names.
	var unknown []string
	for _, name := range filter {
		if !valid[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		known := make([]string, 0, len(BuiltinTools))
		for _, reg := range BuiltinTools {
			known = append(known, reg.Name)
		}
		return nil, fmt.Errorf("unknown built-in tool(s): %s (known tools: %s)",
			strings.Join(unknown, ", "), strings.Join(known, ", "))
	}

	// Build the enabled set.
	enabled := make(map[string]bool, len(filter))
	for _, name := range filter {
		enabled[name] = true
	}

	var result []llm.Tool
	for _, reg := range BuiltinTools {
		if enabled[reg.Name] {
			result = append(result, reg.New())
		}
	}
	return result, nil
}
