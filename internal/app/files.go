package app

import (
	"bytes"
	"path"
	"strings"
)

const (
	fileFormatPDF  = "pdf"
	fileFormatDOCX = "docx"
	fileFormatDOC  = "doc"
)

// storedFileFormat derives the file format from the object key extension.
// Legacy keys end in ".pdf"; unknown extensions are treated as PDF.
func storedFileFormat(key string) string {
	switch ext := strings.TrimPrefix(strings.ToLower(path.Ext(strings.TrimSpace(key))), "."); ext {
	case fileFormatDOCX, fileFormatDOC:
		return ext
	default:
		return fileFormatPDF
	}
}

// storedFileContentType maps a stored file format to its HTTP content type.
func storedFileContentType(format string) string {
	switch format {
	case fileFormatDOCX:
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case fileFormatDOC:
		return "application/msword"
	default:
		return "application/pdf"
	}
}

// storedFileIsValid checks the leading bytes against the format's signature.
// DOCX files are ZIP containers; DOC files use the OLE compound signature.
func storedFileIsValid(key string, prefix []byte) bool {
	switch storedFileFormat(key) {
	case fileFormatDOCX:
		return bytes.HasPrefix(prefix, []byte{'P', 'K', 0x03, 0x04})
	case fileFormatDOC:
		return bytes.HasPrefix(prefix, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	default:
		return bytes.HasPrefix(prefix, []byte("%PDF"))
	}
}

// downloadFileName appends the stored format's extension to a display name.
// Existing known extensions are replaced so a name never ends in "x.pdf.docx".
func downloadFileName(name, format string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case strings.ContainsRune(`/\:*?"<>|`, r):
			return '_'
		default:
			return r
		}
	}, name))
	if name == "" {
		name = "资料"
	}
	lower := strings.ToLower(name)
	for _, ext := range []string{".pdf", ".docx", ".doc"} {
		if strings.HasSuffix(lower, ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "资料"
	}
	runes := []rune(name)
	if len(runes) > 180 {
		name = string(runes[:180])
	}
	return name + "." + format
}
