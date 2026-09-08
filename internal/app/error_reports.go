package app

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

type ErrorReportInput struct {
	EventID    string `json:"event_id"`
	OccurredAt string `json:"occurred_at"`
	AppVersion string `json:"app_version"`
	Platform   string `json:"platform"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	ErrorText  string `json:"error"`
	StackTrace string `json:"stack_trace"`
}

type ErrorReportView struct {
	ID         int64  `json:"id"`
	EventID    string `json:"event_id"`
	UserID     string `json:"user_id,omitempty"`
	DeviceID   string `json:"device_id"`
	StudentID  string `json:"student_id,omitempty"`
	AppVersion string `json:"app_version"`
	Platform   string `json:"platform"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	ErrorText  string `json:"error,omitempty"`
	StackTrace string `json:"stack_trace,omitempty"`
	OccurredAt string `json:"occurred_at"`
	ReceivedAt string `json:"received_at"`
	Ignored    bool   `json:"ignored"`
}

func (a *App) InsertErrorReport(ctx context.Context, p SessionPrincipal, in ErrorReportInput) (bool, error) {
	in.EventID = strings.TrimSpace(in.EventID)
	if in.EventID == "" || len(in.EventID) > 128 {
		return false, Err("invalid_error_report", "错误日志标识无效", http.StatusBadRequest)
	}
	occurred, err := time.Parse(time.RFC3339, strings.TrimSpace(in.OccurredAt))
	if err != nil {
		return false, Err("invalid_error_report_time", "错误日志时间无效", http.StatusBadRequest)
	}
	now := time.Now().UTC()
	if occurred.After(now.Add(10*time.Minute)) || occurred.Before(now.Add(-30*24*time.Hour)) {
		return false, Err("invalid_error_report_time", "错误日志时间无效", http.StatusBadRequest)
	}
	if len(in.AppVersion) > 64 || len(in.Platform) > 32 || len(in.Title) > 128 || len(in.Message) > 4096 || len(in.ErrorText) > 4096 || len(in.StackTrace) > 16384 {
		return false, Err("invalid_error_report", "错误日志内容过长", http.StatusBadRequest)
	}
	identity, err := a.Store.GetIdentityByUser(ctx, p.User.ID, identityProvider)
	if err == sql.ErrNoRows {
		return false, Err("student_identity_missing", "当前用户没有学号身份", http.StatusForbidden)
	}
	if err != nil {
		return false, err
	}
	accepted, err := a.Store.InsertErrorReport(ctx, sqlite.ErrorReportInput{
		EventID: in.EventID, UserID: p.User.ID, DeviceID: p.Device.ID, StudentIDHash: identity.StudentIDHash,
		AppVersion: strings.TrimSpace(in.AppVersion), Platform: strings.TrimSpace(in.Platform), Title: strings.TrimSpace(in.Title),
		Message: in.Message, ErrorText: in.ErrorText, StackTrace: in.StackTrace, OccurredAt: occurred.UTC(), ReceivedAt: now,
	})
	return accepted, err
}

func (a *App) ListErrorReports(ctx context.Context, limit, offset int) ([]ErrorReportView, int, error) {
	items, total, err := a.Store.ListErrorReports(ctx, clampLimit(limit), max0(offset))
	if err != nil {
		return nil, 0, err
	}
	result := make([]ErrorReportView, 0, len(items))
	for _, item := range items {
		studentID := ""
		if item.UserID != "" {
			identity, identityErr := a.Store.GetIdentityByUser(ctx, item.UserID, identityProvider)
			if identityErr == nil {
				studentID, _ = controlcrypto.Decrypt(a.Cfg.EncryptionKey, identity.StudentIDCiphertext)
			}
		}
		result = append(result, ErrorReportView{
			ID: item.ID, EventID: item.EventID, UserID: item.UserID, DeviceID: item.DeviceID, StudentID: studentID,
			AppVersion: item.AppVersion, Platform: item.Platform, Title: item.Title, Message: item.Message,
			ErrorText: item.ErrorText, StackTrace: item.StackTrace,
			OccurredAt: item.OccurredAt.Format(time.RFC3339), ReceivedAt: item.ReceivedAt.Format(time.RFC3339), Ignored: item.Ignored,
		})
	}
	return result, total, nil
}

func (a *App) SetErrorReportStudentIgnored(ctx context.Context, reportID int64, ignored bool, actor string) error {
	if reportID <= 0 {
		return Err("invalid_error_report", "错误报告标识无效", http.StatusBadRequest)
	}
	if err := a.Store.SetErrorReportStudentIgnoredByReportID(ctx, reportID, ignored, time.Now().UTC()); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	action := "error_report_student_allow"
	if ignored {
		action = "error_report_student_ignore"
	}
	return a.Store.AddAudit(ctx, actor, action, "error_report", strconv.FormatInt(reportID, 10), controlcrypto.JSON(map[string]bool{"ignored": ignored}), time.Now().UTC())
}

func (a *App) ClearErrorReports(ctx context.Context, actor string) (int64, error) {
	count, err := a.Store.ClearErrorReports(ctx)
	if err != nil {
		return 0, err
	}
	if err := a.Store.AddAudit(ctx, actor, "error_reports_clear", "error_reports", "all", controlcrypto.JSON(map[string]int64{"deleted": count}), time.Now().UTC()); err != nil {
		return 0, err
	}
	return count, nil
}
