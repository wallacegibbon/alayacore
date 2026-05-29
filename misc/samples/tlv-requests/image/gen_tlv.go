//go:build ignore

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/stream"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: go run gen_tlv.go <prompt> <image1> [image2 ...] > <output>\n")
		os.Exit(1)
	}

	prompt := os.Args[1]

	for _, imgPath := range os.Args[2:] {
		data, err := os.ReadFile(imgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", imgPath, err)
			os.Exit(1)
		}

		mime := mimeType(imgPath)
		b64 := base64.StdEncoding.EncodeToString(data)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mime, b64)

		if _, err := os.Stdout.Write(stream.EncodeTLV(stream.TagUserI, dataURI)); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
	}

	if _, err := os.Stdout.Write(stream.EncodeTLV(stream.TagUserT, prompt)); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}

func mimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
