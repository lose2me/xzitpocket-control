package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"xzitpocket-control/internal/app"
)

// App users upload textbooks/exams for review. The file goes directly to COS
// through a presigned URL; control only issues the ticket, verifies the object
// and exposes the queue to administrators.

func (s *Server) userSubmissionTicket(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	var in app.ResourceSubmissionTicketInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.CreateSubmissionTicket(r.Context(), p, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) userSubmissionComplete(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	out, err := s.App.CompleteSubmission(r.Context(), p, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) userSubmissionAbort(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	if err := s.App.AbortSubmission(r.Context(), p, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"aborted": true})
}

func (s *Server) adminSubmissions(w http.ResponseWriter, r *http.Request, kind string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListSubmissionsForAdmin(r.Context(), kind, limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminSubmissionApprove(w http.ResponseWriter, r *http.Request, kind, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	out, err := s.App.ApproveSubmissionForAdmin(r.Context(), id, kind, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminSubmissionReject(w http.ResponseWriter, r *http.Request, kind, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.RejectSubmissionForAdmin(r.Context(), id, kind, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rejected": true})
}

func (s *Server) adminSubmissionResourceURL(w http.ResponseWriter, r *http.Request, kind, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	ttl := time.Duration(0)
	if raw := r.URL.Query().Get("ttl"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			ttl = time.Duration(seconds) * time.Second
		}
	}
	out, err := s.App.SubmissionResourceURL(r.Context(), id, kind, ttl)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
