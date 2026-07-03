package agent

import "io"

// nopInput is a Reader that always returns EOF.
type nopInput struct{}

func (nopInput) Read(_ []byte) (int, error) { return 0, io.EOF }

// nopOutput is a Writer that discards all data.
type nopOutput struct{}

func (nopOutput) Write(p []byte) (int, error) { return len(p), nil }
