package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
)

// ContributorBindingInput binds published content to a student account.
type ContributorBindingInput struct {
	StudentID string `json:"student_id"`
}

// resolveContributor maps a student ID to the account that owns it. An empty
// student ID means "unbind".
func (a *App) resolveContributor(ctx context.Context, studentID string) (userID, name string, err error) {
	studentID = strings.TrimSpace(studentID)
	if studentID == "" {
		return "", "", nil
	}
	if len(studentID) > 64 || strings.ContainsAny(studentID, "\r\n\x00") {
		return "", "", Err("invalid_student_id", "学号无效", 400)
	}
	hash := controlcrypto.HMACHex(a.Cfg.IDPepper, studentID)
	identity, err := a.Store.GetIdentityByHash(ctx, identityProvider, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", Err("student_not_found", "该学号尚未登录过 App", 404)
	}
	if err != nil {
		return "", "", err
	}
	return identity.UserID, identity.StudentAlias, nil
}

// transferContribution moves one contribution credit between accounts. It is
// best-effort: a counter failure must not undo an attribution change.
func (a *App) transferContribution(ctx context.Context, oldUserID, newUserID string) {
	if oldUserID == newUserID {
		return
	}
	if oldUserID != "" {
		if err := a.Store.AdjustUserContribution(ctx, oldUserID, -1); err != nil {
			a.Logger.Warn("decrement contributor count failed", "error", err, "user", oldUserID)
		}
	}
	if newUserID != "" {
		if err := a.Store.AdjustUserContribution(ctx, newUserID, 1); err != nil {
			a.Logger.Warn("increment contributor count failed", "error", err, "user", newUserID)
		}
	}
}

// BindTextbookContributor sets or clears the student credited for a published
// textbook. Existing contributions follow the content; download totals only
// cover future app downloads because the content counter also includes admin
// and browser downloads.
func (a *App) BindTextbookContributor(ctx context.Context, id, studentID, actor string) (TextbookView, error) {
	item, err := a.requireTextbook(ctx, id)
	if err != nil {
		return TextbookView{}, err
	}
	newUserID, _, err := a.resolveContributor(ctx, studentID)
	if err != nil {
		return TextbookView{}, err
	}
	oldUserID := item.ContributorUserID
	if oldUserID == newUserID {
		return a.textbookViewForAdmin(ctx, item), nil
	}
	if err := a.Store.SetTextbookContributor(ctx, item.ID, newUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TextbookView{}, ErrNotFound
		}
		return TextbookView{}, err
	}
	a.transferContribution(ctx, oldUserID, newUserID)
	item.ContributorUserID = newUserID
	now := time.Now().UTC()
	_ = a.Store.AddAudit(ctx, actor, "textbook_contributor_bind", "textbook", item.ID,
		controlcrypto.JSON(map[string]any{
			"old_user_id": oldUserID,
			"new_user_id": newUserID,
			"student_id":  strings.TrimSpace(studentID),
		}), now)
	return a.textbookViewForAdmin(ctx, item), nil
}

// BindResourceContributor sets or clears the student credited for a published
// exam paper.
func (a *App) BindResourceContributor(ctx context.Context, id, studentID, actor string) (ResourceView, error) {
	item, err := a.requireResource(ctx, id)
	if err != nil {
		return ResourceView{}, err
	}
	newUserID, _, err := a.resolveContributor(ctx, studentID)
	if err != nil {
		return ResourceView{}, err
	}
	oldUserID := item.ContributorUserID
	if oldUserID == newUserID {
		return a.resourceViewForAdmin(ctx, item), nil
	}
	if err := a.Store.SetResourceContributor(ctx, item.ID, newUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResourceView{}, ErrNotFound
		}
		return ResourceView{}, err
	}
	a.transferContribution(ctx, oldUserID, newUserID)
	item.ContributorUserID = newUserID
	now := time.Now().UTC()
	_ = a.Store.AddAudit(ctx, actor, "resource_contributor_bind", "resource", item.ID,
		controlcrypto.JSON(map[string]any{
			"old_user_id": oldUserID,
			"new_user_id": newUserID,
			"student_id":  strings.TrimSpace(studentID),
		}), now)
	return a.resourceViewForAdmin(ctx, item), nil
}
