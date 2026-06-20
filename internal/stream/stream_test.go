package stream

import (
	"bytes"
	"io"
	"testing"
)

func TestEncodeTLV(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		value   string
		wantLen int
		wantTag string
		wantVal string
	}{
		{
			name:    "simple message",
			tag:     TagUserT,
			value:   "Hello, World!",
			wantLen: 6 + 13, // 6 header + 13 value
			wantTag: TagUserT,
			wantVal: "Hello, World!",
		},
		{
			name:    "empty value",
			tag:     TagAssistantT,
			value:   "",
			wantLen: 6, // just header
			wantTag: TagAssistantT,
			wantVal: "",
		},
		{
			name:    "unicode value",
			tag:     TagUserT,
			value:   "你好世界 🌍",
			wantLen: 6 + len("你好世界 🌍"), // 6 header + actual byte length
			wantTag: TagUserT,
			wantVal: "你好世界 🌍",
		},
		{
			name:    "long message",
			tag:     TagAssistantR,
			value:   string(make([]byte, 1000)),
			wantLen: 6 + 1000,
			wantTag: TagAssistantR,
			wantVal: string(make([]byte, 1000)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeTLVStr(tt.tag, tt.value)
			if len(encoded) != tt.wantLen {
				t.Errorf("EncodeTLV() length = %d, want %d", len(encoded), tt.wantLen)
			}

			// Decode and verify
			tag, value, err := ReadTLV(&byteReader{data: encoded})
			if err != nil {
				t.Fatalf("ReadTLV() error = %v", err)
			}
			if tag != tt.wantTag {
				t.Errorf("ReadTLV() tag = %q, want %q", tag, tt.wantTag)
			}
			if string(value) != tt.wantVal {
				t.Errorf("ReadTLV() value = %q, want %q", string(value), tt.wantVal)
			}
		})
	}
}

func TestSliceBuffer(t *testing.T) {
	t.Run("emit and read", func(t *testing.T) {
		input := NewSliceBuffer(10)

		// Write a message
		n, err := input.Write([]byte("test message"))
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if n != len("test message") {
			t.Fatalf("Write() n = %d, want %d", n, len("test message"))
		}

		// Read the message
		buf := make([]byte, 100)
		n, err = input.Read(buf)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if string(buf[:n]) != "test message" {
			t.Errorf("Read() = %q, want %q", string(buf[:n]), "test message")
		}
	})

	t.Run("emit TLV", func(t *testing.T) {
		input := NewSliceBuffer(10)

		// Emit TLV message
		err := WriteTLVStr(input, TagUserT, "Hello")
		if err != nil {
			t.Fatalf("EmitTLV() error = %v", err)
		}

		// Read and decode
		tag, value, err := ReadTLV(input)
		if err != nil {
			t.Fatalf("ReadTLV() error = %v", err)
		}
		if tag != TagUserT {
			t.Errorf("ReadTLV() tag = %q, want %q", tag, TagUserT)
		}
		if string(value) != "Hello" {
			t.Errorf("ReadTLV() value = %q, want %q", string(value), "Hello")
		}
	})

	t.Run("close returns EOF", func(t *testing.T) {
		input := NewSliceBuffer(10)

		// Close the input
		err := input.Close()
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		// Read should return EOF
		buf := make([]byte, 100)
		_, err = input.Read(buf)
		if err != io.EOF {
			t.Errorf("Read() error = %v, want io.EOF", err)
		}
	})

	t.Run("multiple messages", func(t *testing.T) {
		input := NewSliceBuffer(10)

		messages := []struct {
			tag   string
			value string
		}{
			{TagUserT, "first"},
			{TagAssistantT, "second"},
			{TagAssistantR, "third"},
		}

		// Emit all messages
		for _, msg := range messages {
			err := WriteTLVStr(input, msg.tag, msg.value)
			if err != nil {
				t.Fatalf("EmitTLV() error = %v", err)
			}
		}

		// Read and verify all messages
		for _, want := range messages {
			tag, value, err := ReadTLV(input)
			if err != nil {
				t.Fatalf("ReadTLV() error = %v", err)
			}
			if tag != want.tag || string(value) != want.value {
				t.Errorf("ReadTLV() = (%q, %q), want (%q, %q)",
					tag, string(value), want.tag, want.value)
			}
		}
	})
}

func TestWriteTLV(t *testing.T) {
	t.Run("write to buffer", func(t *testing.T) {
		buf := &bytes.Buffer{}
		output := &bufferOutput{buf}

		err := WriteTLVStr(output, TagUserT, "test message")
		if err != nil {
			t.Fatalf("error = %v", err)
		}

		// Verify the written data
		tag, value, err := ReadTLV(&byteReader{data: buf.Bytes()})
		if err != nil {
			t.Fatalf("ReadTLV() error = %v", err)
		}
		if tag != TagUserT {
			t.Errorf("tag = %q, want %q", tag, TagUserT)
		}
		if string(value) != "test message" {
			t.Errorf("value = %q, want %q", string(value), "test message")
		}
	})
}

// byteReader wraps a byte slice to implement io.Reader
type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// bufferOutput wraps a bytes.Buffer to implement io.Writer
type bufferOutput struct {
	*bytes.Buffer
}
