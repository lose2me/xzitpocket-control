package httpapi

import (
	"net/http"
	"strconv"
)

func (s *Server) adminHostStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			days = parsed
		}
	}
	out, err := s.App.HostStats(r.Context(), days, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
