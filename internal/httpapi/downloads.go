package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Admin downloads stream the stored file through nginx with an attachment
// disposition so the browser saves it instead of opening it inline.

func (s *Server) adminTextbookDownload(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	resp, filename, err := s.App.OpenTextbookDownload(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	streamDownload(w, resp, filename)
}

func (s *Server) adminResourceDownload(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	resp, filename, err := s.App.OpenResourceDownload(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	streamDownload(w, resp, filename)
}

func (s *Server) adminSubmissionDownload(w http.ResponseWriter, r *http.Request, kind, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	resp, filename, err := s.App.OpenSubmissionDownload(r.Context(), id, kind, r.URL.Query().Get("file"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	streamDownload(w, resp, filename)
}

func streamDownload(w http.ResponseWriter, resp *http.Response, filename string) {
	defer resp.Body.Close()
	// The global server write timeout is 30 seconds; a large file needs longer.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	w.Header().Set("Content-Type", downloadContentType(filename))
	w.Header().Set("Content-Disposition", downloadContentDisposition(filename))
	w.Header().Set("Cache-Control", "no-store")
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func downloadContentType(filename string) string {
	switch strings.ToLower(path.Ext(filename)) {
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".doc":
		return "application/msword"
	default:
		return "application/pdf"
	}
}

func downloadContentDisposition(filename string) string {
	fallback := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, filename)
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		fallback = "download" + strings.ToLower(path.Ext(filename))
	}
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", fallback, url.PathEscape(filename))
}
