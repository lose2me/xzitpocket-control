package httpapi

import (
	"net/http"
	"strconv"

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

func (s *Server) adminErrorReportsClear(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	count, err := s.App.ClearErrorReports(r.Context(), p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": count})
}

func (s *Server) adminErrorReportStudentIgnored(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	reportID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || reportID <= 0 {
		writeError(w, r, app.Err("invalid_error_report", "错误报告标识无效", http.StatusBadRequest))
		return
	}
	var in struct {
		Ignored *bool `json:"ignored"`
	}
	if err := decodeJSON(r, &in, 16<<10); err != nil || in.Ignored == nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if err := s.App.SetErrorReportStudentIgnored(r.Context(), reportID, *in.Ignored, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": reportID, "ignored": *in.Ignored})
}
