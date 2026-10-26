package httpapi

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"xzitpocket-control/internal/app"
)

// The resource endpoints are administrator-only. Resources are PDFs uploaded
// into a course; a resource row is named after its file.

func (s *Server) adminResources(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListResourcesForAdmin(r.Context(), limit, offset,
		r.URL.Query().Get("q"), r.URL.Query().Get("course_code"),
		r.URL.Query().Get("sort"), r.URL.Query().Get("order"),
		r.URL.Query().Get("archived") == "1")
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminResourceUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, r, app.Err("invalid_upload", "上传内容无效", http.StatusBadRequest))
		return
	}
	if r.MultipartForm == nil {
		writeError(w, r, app.Err("invalid_upload", "上传内容无效", http.StatusBadRequest))
		return
	}
	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		writeError(w, r, app.Err("invalid_upload", "缺少 PDF 文件", http.StatusBadRequest))
		return
	}
	uploads := make([]app.ResourceUpload, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			writeError(w, r, app.Err("invalid_upload", "读取文件失败", http.StatusBadRequest))
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, (80<<20)+1))
		_ = file.Close()
		if err != nil {
			writeError(w, r, app.Err("invalid_upload", "读取文件失败", http.StatusBadRequest))
			return
		}
		uploads = append(uploads, app.ResourceUpload{Name: header.Filename, Data: data})
	}
	out, err := s.App.UploadCourseResources(r.Context(), strings.TrimSpace(r.FormValue("course_code")), uploads, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminResourceContributor(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.ContributorBindingInput
	if err := decodeJSON(r, &in, 4<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.BindResourceContributor(r.Context(), id, in.StudentID, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminResourceManualAnalysis(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, r, app.Err("invalid_upload", "上传内容无效", http.StatusBadRequest))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, app.Err("invalid_upload", "缺少 PDF 文件", http.StatusBadRequest))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (80<<20)+1))
	if err != nil {
		writeError(w, r, app.Err("invalid_upload", "读取文件失败", http.StatusBadRequest))
		return
	}
	item, err := s.App.UploadManualAnalysis(r.Context(), id, data, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) adminResourceDelete(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.DeleteResourceForAdmin(r.Context(), id, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) adminResourceArchive(w http.ResponseWriter, r *http.Request, id string) {
	s.setResourceArchived(w, r, id, true)
}

func (s *Server) adminResourceRestore(w http.ResponseWriter, r *http.Request, id string) {
	s.setResourceArchived(w, r, id, false)
}

func (s *Server) setResourceArchived(w http.ResponseWriter, r *http.Request, id string, archived bool) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	out, err := s.App.SetResourceArchived(r.Context(), id, archived, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminResourceResourceURL(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	ttl := time.Duration(0)
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			ttl = time.Duration(seconds) * time.Second
		}
	}
	out, err := s.App.ResourceURLForAdmin(r.Context(), id, ttl)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
