package app

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ConfigVersionsView struct {
	AppRelease        string `json:"appRelease"`
	SchoolCalendar    string `json:"schoolCalendar"`
	CourseAdjustments string `json:"courseAdjustments"`
}

func configVersion(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func (a *App) GetConfigVersions(ctx context.Context) (ConfigVersionsView, error) {
	release, err := a.Store.GetAppReleaseConfig(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConfigVersionsView{}, err
	}
	calendar, err := a.Store.GetSchoolCalendarConfig(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConfigVersionsView{}, err
	}
	adjustments, err := a.Store.GetCourseAdjustmentsConfig(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConfigVersionsView{}, err
	}
	return ConfigVersionsView{
		AppRelease:        configVersion(release.UpdatedAt),
		SchoolCalendar:    configVersion(calendar.UpdatedAt),
		CourseAdjustments: configVersion(adjustments.UpdatedAt),
	}, nil
}
