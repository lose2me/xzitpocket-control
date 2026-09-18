package httpapi

import (
	"net/http"

	"xzitpocket-control/internal/app"
)

func (s *Server) courseAdjustments(w http.ResponseWriter, r *http.Request) {
	out, err := s.App.GetCourseAdjustments(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCourseAdjustments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	out, err := s.App.GetCourseAdjustments(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCourseAdjustmentsUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.CourseAdjustmentsInput
	if err := decodeJSON(r, &in, 128<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.UpdateCourseAdjustments(r.Context(), in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
