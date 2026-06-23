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

		mime := mimeType(mediaPath)
		b64 := base64.StdEncoding.EncodeToString(data)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mime, b64)
		tag := tagForMIME(mime)

		if _, err := os.Stdout.Write(stream.EncodeTLV(tag, dataURI)); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
	}

	if _, err := os.Stdout.Write(stream.EncodeTLV(stream.TagUserT, prompt)); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}

	// Flush with TagUserEnd to commit the user message.
	if _, err := os.Stdout.Write(stream.EncodeTLV(stream.TagUserEnd, "")); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}

// tagForMIME returns the TLV tag for the given MIME type.
func tagForMIME(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return stream.TagUserI
	case strings.HasPrefix(mime, "video/"):
		return stream.TagUserV
	case strings.HasPrefix(mime, "audio/"):
		return stream.TagUserA
	case strings.HasPrefix(mime, "application/"), strings.HasPrefix(mime, "text/"):
		return stream.TagUserD
	default:
		return stream.TagUserI // fallback
	}
}

func mimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	// Images
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".tiff", ".tif":
		return "image/tiff"

	// Video
	case ".mp4":
		return "video/mp4"
	case ".mpeg", ".mpg":
		return "video/mpeg"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"

	// Audio
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".aac":
		return "audio/aac"
	case ".m4a":
		return "audio/mp4"
	case ".wma":
		return "audio/x-ms-wma"

	// Documents
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".txt":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".md":
		return "text/markdown"

	default:
		return "application/octet-stream"
	}
}
