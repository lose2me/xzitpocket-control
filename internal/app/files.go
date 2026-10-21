package app

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"path"
	"strings"
)

const (
	fileFormatPDF  = "pdf"
	fileFormatDOCX = "docx"
	fileFormatDOC  = "doc"

	booksSubmissionsPrefix  = "submissions/books/"
	papersSubmissionsPrefix = "submissions/papers/"
	booksPublishedPrefix    = "published/books/"
	papersPublishedPrefix   = "published/papers/"
	booksArchivedPrefix     = "achieved/books/"
	papersArchivedPrefix    = "achieved/papers/"
)

// normalizeMD5 validates a lowercase or uppercase 32-character hex MD5.
func normalizeMD5(raw string) (string, bool) {
	hash := strings.ToLower(strings.TrimSpace(raw))
	if len(hash) != 32 {
		return "", false
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", false
		}
	}
	return hash, true
}

// md5Hex is used for content-addressed object names. It is not a security
// primitive; COS also reports the MD5 of a single-part upload as the ETag, so
// direct uploads can be verified without downloading the file again.
func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// submissionObjectKey is the pending-review location for a submission.
func submissionObjectKey(kind, format, hash string) string {
	if kind == "textbook" {
		return booksSubmissionsPrefix + hash + "." + fileFormatPDF
	}
	return papersSubmissionsPrefix + hash + "." + format
}

// publishedObjectKey is the live location for approved content.
func publishedObjectKey(kind, format, hash string) string {
	if kind == "textbook" {
		return booksPublishedPrefix + hash + "." + fileFormatPDF
	}
	return papersPublishedPrefix + hash + "." + format
}

// archivedObjectKey is where archived content waits. Archiving preserves the
// contributor's counters; only deletion removes them.
func archivedObjectKey(kind, format, hash string) string {
	if kind == "textbook" {
		return booksArchivedPrefix + hash + "." + fileFormatPDF
	}
	return papersArchivedPrefix + hash + "." + format
}

// textbookCoverKey names a cover after its textbook PDF; covers are never
// hashed separately.
func textbookCoverKey(hash string) string {
	return booksPublishedPrefix + hash + ".webp"
}

func archivedTextbookCoverKey(hash string) string {
	return booksArchivedPrefix + hash + ".webp"
}

// objectHashFromKey extracts the content hash from a new-style object key.
// Legacy ID-based keys return an empty string.
func objectHashFromKey(key string) string {
	base := path.Base(strings.TrimSpace(key))
	base = strings.TrimSuffix(base, path.Ext(base))
	if hash, ok := normalizeMD5(base); ok {
		return hash
	}
	return ""
}

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
