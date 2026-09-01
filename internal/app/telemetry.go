package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

var allowedEventTypes = map[string]bool{"app_start": true, "foreground": true, "heartbeat": true, "control_login_success": true, "paid_service_open": true, "paid_service_token_success": true, "logout": true}
var allowedPropertyKeys = map[string]bool{"app_version": true, "platform": true, "screen": true, "service": true, "result": true, "source": true, "duration_ms": true, "error_code": true}

type TelemetryEventInput struct {
	EventID    string         `json:"event_id"`
	Type       string         `json:"type"`
	OccurredAt string         `json:"occurred_at"`
	Properties map[string]any `json:"properties"`
}

func validateProperties(properties map[string]any) (string, error) {
	if properties == nil {
		properties = map[string]any{}
	}
	for key, value := range properties {
		if !allowedPropertyKeys[key] || strings.ContainsAny(key, "\r\n") {
			return "", errors.New("property not allowed")
		}
		switch value.(type) {
		case string, bool, float64, int, nil:
		default:
			return "", errors.New("property value not allowed")
		}
	}
	b, err := json.Marshal(properties)
	if err != nil || len(b) > 2048 {
		return "", errors.New("properties too large")
	}
	return string(b), nil
}

func (a *App) InsertTelemetry(ctx context.Context, device DevicePrincipal, session *SessionPrincipal, input []TelemetryEventInput) (map[string]any, error) {
	if len(input) == 0 || len(input) > 200 {
		return nil, Err("invalid_events", "事件数量必须为 1-200", http.StatusBadRequest)
	}
	now := time.Now().UTC()
	events := make([]sqlite.EventInput, 0, len(input))
	userID := ""
	if session != nil {
		userID = session.User.ID
		device = DevicePrincipal{Device: session.Device, Token: session.Token}
	}
	for _, item := range input {
		item.EventID = strings.TrimSpace(item.EventID)
		item.Type = strings.TrimSpace(item.Type)
		if item.EventID == "" || len(item.EventID) > 128 || !allowedEventTypes[item.Type] {
			return nil, Err("invalid_event", "事件参数无效", http.StatusBadRequest)
		}
		occurred, err := time.Parse(time.RFC3339, item.OccurredAt)
		if err != nil || occurred.After(now.Add(10*time.Minute)) || occurred.Before(now.Add(-30*24*time.Hour)) {
			return nil, Err("invalid_event_time", "事件时间无效", http.StatusBadRequest)
		}
		props, err := validateProperties(item.Properties)
		if err != nil {
			return nil, Err("invalid_event_properties", "事件属性无效", http.StatusBadRequest)
		}
		events = append(events, sqlite.EventInput{EventID: item.EventID, UserID: userID, DeviceID: device.Device.ID, Type: item.Type, OccurredAt: occurred, ReceivedAt: now, Properties: props})
	}
	accepted, duplicates, err := a.Store.InsertEvents(ctx, events)
	if err != nil {
		a.Logger.Warn("insert telemetry failed", "error", err)
		return nil, err
	}
	days := map[string]bool{}
	for _, event := range events {
		days[controlcrypto.DayUTC(event.OccurredAt)] = true
	}
	for day := range days {
		if err := a.Store.RebuildDailyMetrics(ctx, day, "xzitpocket"); err != nil {
			a.Logger.Warn("rebuild daily metrics failed", "day", day, "error", err)
		}
	}
	return map[string]any{"accepted": accepted, "duplicates": duplicates}, nil
}

func (a *App) MetricsOverview(ctx context.Context) (sqlite.Overview, error) {
	return a.Store.MetricsOverview(ctx, time.Now().UTC())
}
func (a *App) MetricsSeries(ctx context.Context, days int) ([]sqlite.SeriesPoint, error) {
	return a.Store.MetricsSeries(ctx, days, time.Now().UTC())
}
func (a *App) MetricsBreakdown(ctx context.Context) (sqlite.MetricsBreakdown, error) {
	return a.Store.MetricsBreakdown(ctx, time.Now().UTC())
}

func (a *App) CleanupTelemetry(ctx context.Context) {
	before := time.Now().UTC().AddDate(0, 0, -a.Cfg.EventRetentionDays)
	if n, err := a.Store.CleanupEvents(ctx, before); err != nil {
		a.Logger.Warn("cleanup events failed", "error", err)
	} else if n > 0 {
		a.Logger.Info("cleaned telemetry events", "count", n)
	}
	metricsBefore := time.Now().UTC().AddDate(-2, 0, 0).Format("2006-01-02")
	if n, err := a.Store.CleanupDailyMetrics(ctx, metricsBefore); err != nil {
		a.Logger.Warn("cleanup daily metrics failed", "error", err)
	} else if n > 0 {
		a.Logger.Info("cleaned daily metrics", "count", n)
	}
	if n, err := a.Store.CleanupChallenges(ctx, time.Now().UTC().Add(-24*time.Hour)); err != nil {
		a.Logger.Warn("cleanup challenges failed", "error", err)
	} else if n > 0 {
		a.Logger.Info("cleaned challenges", "count", n)
	}
}

func eventJSON(value any) string { return controlcrypto.JSON(value) }
