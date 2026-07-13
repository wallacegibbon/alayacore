//go:build ignore

package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/tlv"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: go run misc/gen_tlv_request.go <prompt> <media1> [media2 ...]\n")
		os.Exit(1)
	}

	prompt := os.Args[1]

	for _, mediaPath := range os.Args[2:] {
		data, err := os.ReadFile(mediaPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", mediaPath, err)
			os.Exit(1)
		}

		mime := tlv.MimeTypeForPath(mediaPath)
		b64 := base64.StdEncoding.EncodeToString(data)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mime, b64)
		tag := tlv.TagForMIME(mime)

		if _, err := os.Stdout.Write(tlv.EncodeTLV(tag, dataURI)); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
	}

	if _, err := os.Stdout.Write(tlv.EncodeTLV(tlv.TagUserT, prompt)); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}

	// Flush with TagUserEnd to commit the user message.
	if _, err := os.Stdout.Write(tlv.EncodeTLV(tlv.TagUserEnd, "")); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}
