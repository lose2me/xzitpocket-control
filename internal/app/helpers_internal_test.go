package app

import (
	"context"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func TestInternalHelpersWithoutAIState(t *testing.T) {
	a, store := newCourseApp(t)
	// No AI state initialised: the worker helpers are safe no-ops.
	a.wakeAIWorker()
	if a.aiNow().IsZero() {
		t.Fatal("aiNow should still return the current time")
	}
	if a.aiInWindow() || a.aiWindowClosed() {
		t.Fatal("no AI state means no window")
	}
	_, cancel, windowEnd := a.aiCallContext(context.Background())
	cancel()
	if windowEnd.IsZero() {
		t.Fatal("call context should fall back to now")
	}

	// Device-burst risk events use the configured threshold.
	ctx := context.Background()
	cfg := a.cfg()
	cfg.RiskDeviceCount = 1
	a.SetConfigForTesting(cfg)
	now := time.Now().UTC()
	for i, id := range []string{"dev-a", "dev-b"} {
		if err := store.CreateDevice(ctx, sqlite.Device{ID: id, DeviceSerial: id, Installation: id, TokenHash: id, PublicKey: "k", Platform: "android", AppVersion: "1", CreatedAt: now, LastSeenAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.BindUserDevice(ctx, "usr-upload", id, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	a.recordRiskForDevices(ctx, "usr-upload", "dev-b")
	events, err := store.ListRiskEvents(ctx, 10, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "account_device_burst" {
		t.Fatalf("device burst events = %+v", events)
	}
	// A single device stays below the threshold.
	a.recordRiskForDevices(ctx, "usr-upload", "dev-a")
}
