package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/llm"
)

// WriteFileInput represents the input for the write_file tool
type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"required,description=File path to write"`
	Content string `json:"content" jsonschema:"required,description=Content to write to the file"`
}

// NewWriteFileTool creates a tool for writing files
func NewWriteFileTool() llm.Tool {
	return llm.NewTool(
		"write_file",
		"Create a new file or replace the entire content of an existing file. For surgical edits to existing files, prefer edit_file instead.",
	).
		WithSchema(llm.MustGenerateSchema(WriteFileInput{})).
		WithExecute(llm.TypedExecute(executeWriteFile)).
		Build()
}

func executeWriteFile(_ context.Context, args WriteFileInput) ([]llm.ContentPart, error) {
	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if args.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0600); err != nil {
		return nil, err
	}
	return []llm.ContentPart{&llm.TextPart{Text: "File written successfully"}}, nil
}
