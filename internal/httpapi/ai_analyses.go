package httpapi

import (
	"net/http"

	"xzitpocket-control/internal/app"
)

type aiEnqueueInput struct {
	ResourceIDs []string `json:"resource_ids"`
	Modes       []string `json:"modes"`
}

// adminAITasks lists AI analysis jobs.
func (s *Server) adminAITasks(w http.ResponseWriter, r *http.Request) {
	limit, offset := queryPage(r)
	items, total, err := s.App.ListAITasksForAdmin(r.Context(),
		r.URL.Query().Get("status"), r.URL.Query().Get("q"), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

// adminAIEnqueue creates or resets tasks for the selected resources.
func (s *Server) adminAIEnqueue(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in aiEnqueueInput
	if err := decodeJSON(r, &in, 64<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	items, failed, err := s.App.EnqueueAIAnalyses(r.Context(), in.ResourceIDs, in.Modes, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "failed": failed})
}

// adminAITaskDetail returns one job including the generated markdown.
func (s *Server) adminAITaskDetail(w http.ResponseWriter, r *http.Request, id string) {
	item, err := s.App.GetAITaskForAdmin(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// adminAITaskRetry resumes a stopped job.
func (s *Server) adminAITaskRetry(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	item, err := s.App.RetryAITask(r.Context(), id, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// adminAITaskCancel stops a job and its in-flight request.
func (s *Server) adminAITaskCancel(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	item, err := s.App.CancelAITask(r.Context(), id, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// adminAITaskDownload streams the generated analysis PDF.
func (s *Server) adminAITaskDownload(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	resp, filename, err := s.App.OpenAITaskOutput(r.Context(), id, r.URL.Query().Get("mode"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	streamDownload(w, resp, filename)
}
