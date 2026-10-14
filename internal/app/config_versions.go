package app

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ConfigVersionsView struct {
	SchoolCalendar string `json:"schoolCalendar"`
}

func configVersion(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func (a *App) GetConfigVersions(ctx context.Context) (ConfigVersionsView, error) {
	calendar, err := a.Store.GetSchoolCalendarConfig(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConfigVersionsView{}, err
	}
	return ConfigVersionsView{
		SchoolCalendar: configVersion(calendar.UpdatedAt),
	}, nil
}
