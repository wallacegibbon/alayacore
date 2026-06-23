package plainio

import (
	"bufio"
	"io"
	"strings"

	"github.com/alayacore/alayacore/internal/stream"
)

// readPrompts reads lines from stdin and emits them as TLV messages.
// Lines ending with `\` are continued on the next line (backslash-escaped newline).
// Returns nil on EOF (Ctrl-D) or an error.
func readPrompts(input io.Writer, reader io.Reader) error {
	scanner := bufio.NewReader(reader)
	var prompt strings.Builder

	for {
		line, err := scanner.ReadString('\n')
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

// sendPrompt writes a prompt to the TLV stream, followed by MB to flush.
// Commands (starting with ':') are sent without MB. Returns the first
// write error, if any.
func sendPrompt(input io.Writer, text string) error {
	if err := stream.WriteTLV(input, stream.TagUserT, text); err != nil {
		return err
	}
	if !strings.HasPrefix(text, ":") {
		if err := stream.WriteTLV(input, stream.TagUserEnd, ""); err != nil {
			return err
		}
	}
	return nil
}
