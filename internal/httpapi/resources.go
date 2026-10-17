package httpapi

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"xzitpocket-control/internal/app"
)

// The resource endpoints are administrator-only: resources are uploaded and
// associated with courses by hand in the console.

func (s *Server) adminResources(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListResourcesForAdmin(r.Context(), limit, offset,
		r.URL.Query().Get("q"), r.URL.Query().Get("sort"), r.URL.Query().Get("order"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminResourceCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.ResourceWriteInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.CreateResourceForAdmin(r.Context(), in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) adminResourceUpdate(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.ResourceWriteInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.UpdateResourceForAdmin(r.Context(), id, in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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

func (s *Server) adminResourceUploadPdf(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
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
	out, err := s.App.UploadResourcePdf(r.Context(), id, data, p.Admin.ID)
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
	out, err := s.App.ResourceURL(r.Context(), id, ttl)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
