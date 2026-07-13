package tlv

import (
	"path/filepath"
	"strings"
)

var mimeMap = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp",
	".svg": "image/svg+xml",
	".mp4": "video/mp4", ".mpeg": "video/mpeg", ".mpg": "video/mpeg",
	".avi": "video/x-msvideo", ".mov": "video/quicktime",
	".webm": "video/webm", ".mkv": "video/x-matroska",
	".mp3": "audio/mpeg", ".wav": "audio/wav",
	".ogg": "audio/ogg", ".flac": "audio/flac",
	".aac": "audio/aac", ".m4a": "audio/mp4",
	".wma": "audio/x-ms-wma",
	".pdf": "application/pdf",
	".txt": "text/plain", ".md": "text/plain",
}

// MimeTypeForPath returns the MIME type for a file based on its extension.
func MimeTypeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if mime, ok := mimeMap[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}

// TagForPath returns the TLV tag (UI, UV, UA, or UD) for a file based on its extension.
func TagForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return TagUserI
	case ".mp4", ".mpeg", ".mpg", ".avi", ".mov", ".webm", ".mkv":
		return TagUserV
	case ".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".wma":
		return TagUserA
	default:
		return TagUserD
	}
}

// TagForMIME returns the TLV tag for a MIME type string.
func TagForMIME(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return TagUserI
	case strings.HasPrefix(mime, "video/"):
		return TagUserV
	case strings.HasPrefix(mime, "audio/"):
		return TagUserA
	case strings.HasPrefix(mime, "application/"), strings.HasPrefix(mime, "text/"):
		return TagUserD
	default:
		return TagUserI
	}
}

// MediaLabel returns the display label for a media tag.
func MediaLabel(tag string) string {
	switch tag {
	case TagUserI:
		return "📷 Image"
	case TagUserV:
		return "🎬 Video"
	case TagUserA:
		return "🎵 Audio"
	case TagUserD:
		return "📄 Document"
	}
	return ""
}
