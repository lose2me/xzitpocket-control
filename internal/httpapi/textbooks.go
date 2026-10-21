package httpapi

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"xzitpocket-control/internal/app"
)

// The textbook endpoints are administrator-only: clients report textbooks with
// their courses through POST /api/v1/courses.

func (s *Server) adminTextbooks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListTextbooksForAdmin(r.Context(), limit, offset,
		r.URL.Query().Get("q"), r.URL.Query().Get("sort"), r.URL.Query().Get("order"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminTextbookCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.TextbookWriteInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.CreateTextbookForAdmin(r.Context(), in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) adminTextbookUpdate(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.TextbookWriteInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.UpdateTextbookForAdmin(r.Context(), id, in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminTextbookDelete(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.DeleteTextbookForAdmin(r.Context(), id, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) adminTextbookUploadPdf(w http.ResponseWriter, r *http.Request, id string) {
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
	out, err := s.App.UploadTextbookPdf(r.Context(), id, data, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminTextbookContributor(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.ContributorBindingInput
	if err := decodeJSON(r, &in, 4<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.BindTextbookContributor(r.Context(), id, in.StudentID, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminTextbookUploadCover(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil {
		writeError(w, r, app.Err("invalid_upload", "读取封面失败", http.StatusBadRequest))
		return
	}
	out, err := s.App.UploadTextbookCover(r.Context(), id, data, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminTextbookResourceURL(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	ttl := time.Duration(0)
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			ttl = time.Duration(seconds) * time.Second
		}
	}
	out, err := s.App.TextbookResourceURL(r.Context(), id, r.URL.Query().Get("kind"), ttl)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
