package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMetricsBreakdownFeatureUsage(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	device := Device{ID: "dev_1", DeviceSerial: "serial-1", Installation: "inst-1", TokenHash: "hash", PublicKey: "key", Platform: "android", AppVersion: "1.0.0", CreatedAt: now, LastSeenAt: now}
	if err := store.CreateDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	events := []EventInput{
		{EventID: "e1", DeviceID: "dev_1", Type: "service_open", OccurredAt: now, ReceivedAt: now, Properties: `{"screen":"power"}`},
		{EventID: "e2", DeviceID: "dev_1", Type: "service_open", OccurredAt: now, ReceivedAt: now, Properties: `{"screen":"exams"}`},
		{EventID: "e3", DeviceID: "dev_1", Type: "library_open", OccurredAt: now, ReceivedAt: now, Properties: `{"screen":"learning_center"}`},
		{EventID: "e4", DeviceID: "dev_1", Type: "share_code", OccurredAt: now, ReceivedAt: now, Properties: `{"source":"create"}`},
		{EventID: "e5", DeviceID: "dev_1", Type: "app_start", OccurredAt: now, ReceivedAt: now, Properties: `{}`},
		{EventID: "e6", DeviceID: "dev_1", Type: "control_login_success", OccurredAt: now, ReceivedAt: now, Properties: `{}`},
		{EventID: "e7", DeviceID: "dev_1", Type: "logout", OccurredAt: now, ReceivedAt: now, Properties: `{}`},
	}
	if _, _, err := store.InsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	breakdown, err := store.MetricsBreakdown(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"power": 1, "exams": 1, "learning_center": 1, "share_code": 1}
	if len(breakdown.FeatureUsage) != len(want) {
		t.Fatalf("feature usage = %#v, want %#v", breakdown.FeatureUsage, want)
	}
	for key, value := range want {
		if breakdown.FeatureUsage[key] != value {
			t.Fatalf("feature usage[%s] = %d, want %d", key, breakdown.FeatureUsage[key], value)
		}
	}
}
