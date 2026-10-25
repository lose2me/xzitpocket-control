package app

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"xzitpocket-control/internal/config"
	controlcrypto "xzitpocket-control/internal/crypto"
)

// overviewScaleMinPercent / maxPercent bound the admin-configurable dashboard
// display multiplier (100 = real values, 200 = double).
const (
	overviewScaleMinPercent = 0
	overviewScaleMaxPercent = 100000
)

// OverviewScalePercent returns the current dashboard display multiplier.
func (a *App) OverviewScalePercent() int {
	return a.cfg().OverviewScalePercent
}

// SetOverviewScalePercent stores the multiplier in app_settings and applies it
// immediately, so the dashboard honours it without a restart.
func (a *App) SetOverviewScalePercent(ctx context.Context, percent int, actor string) error {
	if percent < overviewScaleMinPercent || percent > overviewScaleMaxPercent {
		return Err("invalid_scale_percent", "显示比例必须在 0 到 100000 之间", http.StatusBadRequest)
	}
	if err := a.applyRuntimeUpdates(ctx, map[string]string{
		config.SettingOverviewScalePercent: strconv.Itoa(percent),
	}); err != nil {
		return err
	}
	return a.Store.AddAudit(ctx, actor, "overview_display_update", "overview_display", "1", controlcrypto.JSON(map[string]int{"scale_percent": percent}), time.Now().UTC())
}
