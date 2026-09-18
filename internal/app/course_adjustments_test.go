package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestValidateCourseAdjustmentsInput(t *testing.T) {
	encoded, normalized, err := validateCourseAdjustmentsInput(CourseAdjustmentsInput{
		Adjustments: map[string]string{"20260916": "20260917"},
	})
	if err != nil {
		t.Fatalf("valid course adjustment rejected: %v", err)
	}
	if normalized["20260916"] != "20260917" || encoded == "" {
		t.Fatalf("unexpected normalized config: %q %#v", encoded, normalized)
	}
	for _, invalid := range []map[string]string{
		{"2026091": "20260917"},
		{"20260230": "20260917"},
		{"20260916": "2026-09-17"},
	} {
		_, _, err := validateCourseAdjustmentsInput(CourseAdjustmentsInput{Adjustments: invalid})
		apiErr, ok := err.(*APIError)
		if !ok || apiErr.Status != http.StatusBadRequest {
			t.Fatalf("invalid config %#v returned %v, want HTTP 400", invalid, err)
		}
	}
	if _, _, err := validateCourseAdjustmentsInput(CourseAdjustmentsInput{
		Adjustments: map[string]string{"20260920": ""},
	}); err != nil {
		t.Fatalf("empty source should clear a target date: %v", err)
	}
	if _, _, err := validateCourseAdjustmentsInput(CourseAdjustmentsInput{
		Adjustments: map[string]string{"20260916": "20260917", "20260917": "20260917"},
	}); err != nil {
		t.Fatalf("the same source may overwrite multiple targets: %v", err)
	}
}

func TestCourseAdjustmentsInputAcceptsBareMap(t *testing.T) {
	var input CourseAdjustmentsInput
	if err := json.Unmarshal([]byte(`{"20260916":"20260917"}`), &input); err != nil {
		t.Fatalf("decode bare map: %v", err)
	}
	if input.Adjustments["20260916"] != "20260917" {
		t.Fatalf("unexpected bare map: %#v", input.Adjustments)
	}
}
