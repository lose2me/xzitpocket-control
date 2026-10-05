package httpapi

import (
	"net/http"

	"xzitpocket-control/internal/app"
)

func (s *Server) createShareCode(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	var in app.ShareCodeCreateInput
	if err := decodeJSON(r, &in, 600<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.CreateShareCode(r.Context(), in, p.User.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) readShareCode(w http.ResponseWriter, r *http.Request, code string) {
	data, meta, err := s.App.ReadShareCode(r.Context(), code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":      data,
		"createdAt": meta.CreatedAt,
		"expiresAt": meta.ExpiresAt,
	})
}

func (s *Server) adminShareCodes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListShareCodes(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminShareCodeDetail(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	view, data, err := s.App.GetShareCode(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shareCode": view, "data": data})
}

func (s *Server) adminCreateShareCode(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.ShareCodeCreateInput
	if err := decodeJSON(r, &in, 600<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.CreateShareCode(r.Context(), in, "")
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = s.App.Store.AddAudit(r.Context(), p.Admin.ID, "share_code_create", "share_code", "", "{}", out.CreatedAt)
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) adminDeleteShareCode(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	if err := s.App.DeleteShareCode(r.Context(), id, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
