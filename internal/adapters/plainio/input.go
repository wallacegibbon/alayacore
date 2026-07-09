package plainio

import (
	"bufio"
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/tlv"
)

// readPrompts reads lines from stdin and emits them as TLV messages.
// Lines ending with `\` are continued on the next line (backslash-escaped newline).
// Returns nil on EOF (Ctrl-D), a read error, or when done is closed.
// When done is closed, any line already buffered in bufio.Reader is discarded.
func readPrompts(done <-chan struct{}, input io.Writer, reader io.Reader) error {
	scanner := bufio.NewReader(reader)
	var prompt strings.Builder

	for {
		line, err := scanner.ReadString('\n')

		// Check for cancellation before processing any data.
		// This ensures buffered lines (bufio.Reader internal buffer)
		// are discarded when done is closed, even if the underlying
		// file descriptor was already closed.
		select {
		case <-done:
			return nil
		default:
		}

		if err != nil {
			if err == io.EOF {
				if prompt.Len() > 0 || len(line) > 0 {
					prompt.WriteString(line)
					text := strings.TrimRight(prompt.String(), "\r\n")
					if text != "" {
						if err = sendPrompt(input, text); err != nil {
							return err
						}
					}
				}
				return nil
			}
			return err
		}

		// Check if line ends with backslash (escaped newline)
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasSuffix(trimmed, "\\") {
			prompt.WriteString(trimmed[:len(trimmed)-1])
			prompt.WriteString("\n")
			continue
		}

		// Complete prompt: accumulated + current line
		prompt.WriteString(trimmed)
		text := prompt.String()
		prompt.Reset()

		if text == "" {
			continue
		}

		// Intercept :quit/:q — handled locally, not by the session
		if text == ":quit" || text == ":q" {
			return nil
		}

		if err := sendPrompt(input, text); err != nil {
			return err
		}
	}
}

// sendPrompt writes a prompt to the TLV stream, followed by UE to flush.
// Commands (starting with ':') are sent without UE. Returns the first
// write error, if any.
func sendPrompt(input io.Writer, text string) error {
	if err := tlv.WriteTLV(input, tlv.TagUserT, text); err != nil {
		return err
	}
	if !strings.HasPrefix(text, ":") {
		if err := tlv.WriteTLV(input, tlv.TagUserEnd, ""); err != nil {
			return err
		}
	}
	return nil
}
