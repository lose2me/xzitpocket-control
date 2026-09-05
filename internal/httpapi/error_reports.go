package httpapi

import (
	"net/http"

	"xzitpocket-control/internal/app"
)

func (s *Server) errorReport(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	var in app.ErrorReportInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	accepted, err := s.App.InsertErrorReport(r.Context(), p, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": accepted})
}

func (s *Server) adminErrorReports(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListErrorReports(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}
