package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

var courseAdjustmentDatePattern = regexp.MustCompile(`^\d{8}$`)

type CourseAdjustmentsInput struct {
	Adjustments map[string]string `json:"adjustments"`
}

// UnmarshalJSON accepts both the API wrapper ({"adjustments": {...}}) and a
// bare date map, which keeps the JSON editor convenient while retaining a
// stable wrapped request contract for the web client.
func (in *CourseAdjustmentsInput) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["adjustments"]; ok {
		return json.Unmarshal(raw, &in.Adjustments)
	}
	return json.Unmarshal(data, &in.Adjustments)
}

type CourseAdjustmentsView struct {
	Adjustments map[string]string `json:"adjustments"`
	UpdatedAt   string            `json:"updatedAt,omitempty"`
}

func validateCourseAdjustmentsInput(in CourseAdjustmentsInput) (string, map[string]string, error) {
	if len(in.Adjustments) > 1000 {
		return "", nil, Err("invalid_course_adjustments", "课程调整配置数量过多", http.StatusBadRequest)
	}
	normalized := make(map[string]string, len(in.Adjustments))
	for destination, original := range in.Adjustments {
		if !courseAdjustmentDatePattern.MatchString(destination) {
			return "", nil, Err("invalid_course_adjustments", "日期必须是 YYYYMMDD 格式", http.StatusBadRequest)
		}
		if !validCompactDate(destination) {
			return "", nil, Err("invalid_course_adjustments", "课程调整日期无效", http.StatusBadRequest)
		}
		original = strings.TrimSpace(original)
		if original != "" {
			if !courseAdjustmentDatePattern.MatchString(original) || !validCompactDate(original) {
				return "", nil, Err("invalid_course_adjustments", "课程调整日期无效", http.StatusBadRequest)
			}
		}
		normalized[destination] = original
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", nil, err
	}
	return string(encoded), normalized, nil
}

func validCompactDate(value string) bool {
	parsed, err := time.Parse("20060102", value)
	return err == nil && parsed.Format("20060102") == value
}

func courseAdjustmentsView(config sqlite.CourseAdjustmentsConfig, adjustments map[string]string) CourseAdjustmentsView {
	if adjustments == nil {
		adjustments = map[string]string{}
	}
	// Copy so callers cannot mutate the view after it has been returned.
	copyMap := make(map[string]string, len(adjustments))
	keys := make([]string, 0, len(adjustments))
	for key, value := range adjustments {
		keys = append(keys, key)
		copyMap[key] = value
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(copyMap))
	for _, key := range keys {
		ordered[key] = copyMap[key]
	}
	out := CourseAdjustmentsView{Adjustments: ordered}
	if !config.UpdatedAt.IsZero() {
		out.UpdatedAt = config.UpdatedAt.Format(time.RFC3339)
	}
	return out
}

func (a *App) GetCourseAdjustments(ctx context.Context) (CourseAdjustmentsView, error) {
	config, err := a.Store.GetCourseAdjustmentsConfig(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return CourseAdjustmentsView{}, ErrNotFound
	}
	if err != nil {
		return CourseAdjustmentsView{}, err
	}
	var adjustments map[string]string
	if err := json.Unmarshal([]byte(config.AdjustmentsJSON), &adjustments); err != nil {
		return CourseAdjustmentsView{}, Err("invalid_course_adjustments", "课程调整配置损坏", http.StatusInternalServerError)
	}
	_, normalized, validationErr := validateCourseAdjustmentsInput(CourseAdjustmentsInput{Adjustments: adjustments})
	if validationErr != nil {
		return CourseAdjustmentsView{}, Err("invalid_course_adjustments", "课程调整配置损坏", http.StatusInternalServerError)
	}
	return courseAdjustmentsView(config, normalized), nil
}

func (a *App) UpdateCourseAdjustments(ctx context.Context, in CourseAdjustmentsInput, actor string) (CourseAdjustmentsView, error) {
	encoded, adjustments, err := validateCourseAdjustmentsInput(in)
	if err != nil {
		return CourseAdjustmentsView{}, err
	}
	now := time.Now().UTC()
	if err := a.Store.UpdateCourseAdjustmentsConfig(ctx, sqlite.CourseAdjustmentsConfig{AdjustmentsJSON: encoded, UpdatedAt: now}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CourseAdjustmentsView{}, ErrNotFound
		}
		return CourseAdjustmentsView{}, err
	}
	_ = a.Store.AddAudit(ctx, actor, "course_adjustments_update", "course_adjustments", "current",
		controlcrypto.JSON(map[string]any{"count": len(adjustments)}), now)
	return courseAdjustmentsView(sqlite.CourseAdjustmentsConfig{AdjustmentsJSON: encoded, UpdatedAt: now}, adjustments), nil
}
