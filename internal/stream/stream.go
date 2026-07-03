// Package stream provides the SliceBuffer IO bridge used to connect
// adapters (terminal/plainio/rawio) with the agent session.
//
// SliceBuffer is an io.ReadWriteCloser that bridges slice-at-a-time writes
// with byte-at-a-time reads. It is the primary mechanism for sending
// TLV-encoded messages between components running in different goroutines.
package stream

import (
	"io"
	"sync"
)

// SliceBuffer is an io.ReadWriteCloser that bridges slice-at-a-time writes
// with byte-at-a-time reads. Write copies each slice and sends it
// atomically via a channel (the caller, e.g. io.Copy, may reuse its
// buffer); Read uses an internal buffer to reassemble slices into a
// byte stream so callers like io.ReadFull get continuous byte access
// without losing slice boundaries.
type SliceBuffer struct {
	ch        chan []byte
	buf       []byte
	closeOnce sync.Once
}

// NewSliceBuffer creates a SliceBuffer with the given channel buffer size.
func NewSliceBuffer(bufferSize int) *SliceBuffer {
	return &SliceBuffer{ch: make(chan []byte, bufferSize)}
}

// Close closes the input channel, causing Read to return EOF.
// It is safe to call Close multiple times.
func (b *SliceBuffer) Close() error {
	b.closeOnce.Do(func() { close(b.ch) })
	return nil
}

// Read implements io.Reader.
//
// Writes arrive as atomic slices via the channel; Read copies as much as
// fits into p and buffers the rest so callers like io.ReadFull see a
// continuous byte stream.
func (b *SliceBuffer) Read(p []byte) (n int, err error) {
	if len(b.buf) > 0 {
		n = copy(p, b.buf)
		b.buf = b.buf[n:]
		return n, nil
	}

	msg, ok := <-b.ch
	if !ok {
		return 0, io.EOF
	}

	b.buf = msg
	n = copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

// Write implements io.Writer. Each call sends a copy of p as a single
// atomic slice to the channel. Safe for concurrent use.
// A copy is required because the caller (e.g. io.Copy) may reuse the
// underlying buffer on subsequent writes, and the data must remain valid
// until the reader goroutine consumes it from the channel.
func (b *SliceBuffer) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	copy(buf, p)
	b.ch <- buf
	return len(p), nil
}

// NopInput is a Reader that always returns EOF.
type NopInput struct{}

func (n *NopInput) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

// NopOutput is a Writer that discards all data.
type NopOutput struct{}

func (n *NopOutput) Write(p []byte) (int, error) {
	return len(p), nil
}
