package httpapi

import "net/http"

func (s *Server) configVersions(w http.ResponseWriter, r *http.Request) {
	out, err := s.App.GetConfigVersions(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
