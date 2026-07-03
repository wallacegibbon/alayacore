package terminal

// nopWriteCloser is a WriteCloser that discards all data.
// Used in tests that need an io.WriteCloser but don't read from it.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
