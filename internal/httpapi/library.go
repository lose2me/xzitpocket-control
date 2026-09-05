package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"xzitpocket-control/internal/app"
)

func (s *Server) questionBanks(w http.ResponseWriter, r *http.Request) {
	p, admin, ok := s.questionReader(w, r)
	if !ok {
		return
	}
	if !admin && !libraryUserAvailable(w, r, p) {
		return
	}
	limit, offset := queryPage(r)
	var items []app.QuestionBankSummaryView
	var total int
	var err error
	if admin {
		items, total, err = s.App.ListQuestionBanks(r.Context(), limit, offset, "active")
	} else {
		items, total, err = s.App.ListQuestionBanksForUser(r.Context(), limit, offset, p.User.ID)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) questionBank(w http.ResponseWriter, r *http.Request, id string) {
	p, admin, ok := s.questionReader(w, r)
	if !ok {
		return
	}
	if !admin && !libraryUserAvailable(w, r, p) {
		return
	}
	var out app.QuestionBankResponse
	var err error
	if admin {
		out, err = s.App.GetQuestionBank(r.Context(), id)
	} else {
		out, err = s.App.GetQuestionBankForUser(r.Context(), id, p.User.ID)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Published question content is available to a signed-in user. Administrators
// can preview protected banks with their existing admin session.
func (s *Server) questionReader(w http.ResponseWriter, r *http.Request) (app.SessionPrincipal, bool, bool) {
	if token := sessionToken(r); token != "" {
		if principal, err := s.App.AuthenticateSession(r.Context(), token); err == nil {
			return principal, false, true
		}
	}
	if token := adminToken(r); token != "" {
		if _, err := s.App.AuthenticateAdmin(r.Context(), token); err == nil {
			return app.SessionPrincipal{}, true, true
		}
	}
	writeError(w, r, app.ErrUnauthorized)
	return app.SessionPrincipal{}, false, false
}

func libraryUserAvailable(w http.ResponseWriter, r *http.Request, p app.SessionPrincipal) bool {
	if p.User.Status == "active" {
		return true
	}
	writeError(w, r, app.Err("user_unavailable", "您的账户被暂时禁用", http.StatusForbidden))
	return false
}

func (s *Server) redeemLibraryCDK(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authSession(w, r)
	if !ok {
		return
	}
	if !libraryUserAvailable(w, r, p) {
		return
	}
	var in struct {
		Code           string `json:"code"`
		QuestionBankID string `json:"question_bank_id"`
	}
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.RedeemLibraryCDK(r.Context(), p, in.Code, in.QuestionBankID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminLibraryCDKs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	items, total, err := s.App.ListLibraryCDKs(r.Context(), limit, offset, r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminLibraryCDKCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in app.LibraryCDKCreateInput
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if in.Count <= 1 {
		out, err := s.App.CreateLibraryCDK(r.Context(), in, p.Admin.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}
	items, err := s.App.CreateLibraryCDKs(r.Context(), in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) adminLibraryCDKStatus(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	if err := s.App.SetLibraryCDKStatus(r.Context(), id, strings.TrimSpace(in.Status), p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": in.Status})
}

func (s *Server) adminQuestionBanks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	limit, offset := queryPage(r)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	items, total, err := s.App.ListQuestionBanks(r.Context(), limit, offset, status)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) adminQuestionBankDetail(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authAdmin(w, r); !ok {
		return
	}
	out, err := s.App.GetAdminQuestionBank(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminQuestionBankCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	in, err := decodeQuestionBankInput(r)
	if err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.CreateQuestionBank(r.Context(), in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) adminQuestionBankUpdate(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	in, err := decodeQuestionBankInput(r)
	if err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	out, err := s.App.UpdateQuestionBank(r.Context(), id, in, p.Admin.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminQuestionBankStatus(w http.ResponseWriter, r *http.Request, id string) {
	p, ok := s.authAdmin(w, r)
	if !ok || !checkAdminCSRF(w, r) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &in, 16<<10); err != nil {
		writeError(w, r, app.Err("invalid_json", "请求格式无效", http.StatusBadRequest))
		return
	}
	status := strings.TrimSpace(in.Status)
	if err := s.App.SetQuestionBankStatus(r.Context(), id, status, p.Admin.ID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status})
}

func decodeQuestionBankInput(r *http.Request) (app.QuestionBankInput, error) {
	const maxQuestionBankBody = 2 << 20
	var envelope struct {
		QuestionBank app.QuestionBankInput `json:"questionBank"`
	}
	if err := decodeJSON(r, &envelope, maxQuestionBankBody); err != nil {
		return app.QuestionBankInput{}, err
	}
	if envelope.QuestionBank.Name == "" && envelope.QuestionBank.Questions == nil {
		return app.QuestionBankInput{}, errors.New("questionBank is required")
	}
	return envelope.QuestionBank, nil
}
