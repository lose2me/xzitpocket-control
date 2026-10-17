package httpapi

import (
	"net/http"

	"xzitpocket-control/internal/app"
)

// requireSuperAdmin authenticates and requires the wildcard administrator.
func (s *Server) requireSuperAdmin(w http.ResponseWriter, r *http.Request) (app.AdminPrincipal, bool) {
	p, ok := s.authAdmin(w, r)
	if !ok {
		return p, false
	}
	if !p.Admin.IsSuperAdmin() {
		writeError(w, r, app.Err("forbidden", "仅超级管理员可管理管理员", http.StatusForbidden))
		return p, false
	}
	return p, true
}

func (s *Server) adminAdmins(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSuperAdmin(w, r); !ok {
		return
	}
	items, err := s.App.ListAdminsForAdmin(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminAdminCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireSuperAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.AdminWriteInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.CreateAdminForAdmin(r.Context(), in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) adminAdminUpdate(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.requireSuperAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.AdminWriteInput
	if err := decodeJSON(r, &in, 32<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.UpdateAdminForAdmin(r.Context(), id, in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminAdminDelete(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.requireSuperAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.DeleteAdminForAdmin(r.Context(), id, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
