package httpapi

import (
	"net/http"

	"xzitpocket-control/internal/app"
)

// appRelease serves the small unauthenticated contract used by future APP
// clients to check whether a newer build is available.
func (s *Server) appRelease(w http.ResponseWriter, r *http.Request) {
	out, err := s.App.GetAppReleaseConfig(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminAppRelease(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	out, err := s.App.GetAppReleaseConfig(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminAppReleaseUpdate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.AppReleaseInput
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.UpdateAppReleaseConfig(r.Context(), in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
