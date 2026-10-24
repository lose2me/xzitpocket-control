package app

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ConfigVersionsView struct {
	SchoolCalendar string `json:"schoolCalendar"`
	// ResourceCenter changes whenever any resource-centre content changes, so
	// app clients can skip the course-list request entirely when it matches.
	ResourceCenter string `json:"resourceCenter"`
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
	resourceCenter, err := a.Store.ResourceCenterRevision(ctx)
	if err != nil {
		return ConfigVersionsView{}, err
	}
	return ConfigVersionsView{
		SchoolCalendar: configVersion(calendar.UpdatedAt),
		ResourceCenter: resourceCenter,
	}, nil
}
