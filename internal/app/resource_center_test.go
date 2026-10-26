package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"xzitpocket-control/internal/store/sqlite"
)

func TestResourceCentreGroupsCourseContent(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := store.CreateCourse(ctx, sqlite.CourseInput{Code: "C1", Name: "高等数学"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCourse(ctx, sqlite.CourseInput{Code: "C2", Name: "空课程"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateQuestionBank(ctx, QuestionBankInput{
		Name: "随堂测验", CourseCodes: []string{"C1"},
		Questions: []QuestionInput{{QuestionNumber: 1, Type: "填空题", Title: "题", QuestionText: "题干", CorrectAnswer: "答案"}},
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "期末试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "resources/res-1/original.pdf", now); err != nil {
		t.Fatal(err)
	}
	textbook, err := store.CreateTextbook(ctx, sqlite.TextbookInput{Name: "高等数学（上）", Tags: "同济版", CourseCodes: []string{"C1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTextbookPdfKey(ctx, textbook.ID, "books/tbx-1/original.pdf", now); err != nil {
		t.Fatal(err)
	}

	courses, err := a.ListResourceCourses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(courses) != 2 {
		t.Fatalf("courses = %#v, want every course including empty ones", courses)
	}
	var got ResourceCourseView
	for _, course := range courses {
		if course.Code == "C1" {
			got = course
		}
	}
	if got.Code == "" {
		t.Fatalf("courses = %#v, want C1", courses)
	}
	if got.Code != "C1" || got.QuestionBankCount != 1 || got.ResourceCount != 1 || got.TextbookCount != 1 {
		t.Fatalf("course = %#v", got)
	}
	if got.Version == "" {
		t.Fatal("course version must be set")
	}

	resources, total, version, err := a.ListCourseResourcesForUser(ctx, "C1")
	if err != nil || total != 1 || len(resources) != 1 || resources[0].Name != "期末试卷.pdf" {
		t.Fatalf("resources = %#v, total = %d, err = %v", resources, total, err)
	}
	if version == "" || version != got.Version {
		t.Fatalf("resource version = %q, course version = %q", version, got.Version)
	}
	textbooks, total, textbookVersion, err := a.ListCourseTextbooksForUser(ctx, "C1")
	if err != nil || total != 1 || len(textbooks) != 1 || textbooks[0].Name != "高等数学（上）" {
		t.Fatalf("textbooks = %#v, total = %d, err = %v", textbooks, total, err)
	}
	if textbookVersion != version {
		t.Fatalf("textbook version = %q, resource version = %q", textbookVersion, version)
	}
	if _, _, _, err := a.ListCourseResourcesForUser(ctx, ""); err == nil {
		t.Fatal("empty course code should be rejected")
	}

	// Adding a resource changes the course version while its counts stay the same.
	if _, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "补考试卷.pdf", CourseCode: "C1"}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	courses, err = a.ListResourceCourses(ctx)
	if err != nil {
		t.Fatalf("courses = %#v, err = %v", courses, err)
	}
	var updated ResourceCourseView
	for _, course := range courses {
		if course.Code == "C1" {
			updated = course
		}
	}
	if updated.Version == got.Version {
		t.Fatal("course version did not change after adding a resource")
	}
	if _, _, updatedVersion, err := a.ListCourseResourcesForUser(ctx, "C1"); err != nil || updatedVersion != updated.Version {
		t.Fatalf("updated version = %q, want %q, err = %v", updatedVersion, updated.Version, err)
	}
	versions, err := a.GetConfigVersions(ctx)
	if err != nil || versions.ResourceCenter == "" {
		t.Fatalf("config versions = %#v, err = %v", versions, err)
	}
}

func TestCourseRevisionAdvancesWithinTheSameMillisecond(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if _, err := store.CreateCourse(ctx, sqlite.CourseInput{Code: "C1", Name: "高等数学"}, now); err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "期末试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "resources/res-1/original.pdf", now); err != nil {
		t.Fatal(err)
	}
	first, err := store.CourseContentRevision(ctx, "C1")
	if err != nil {
		t.Fatal(err)
	}
	// Same millisecond, different content: the monotonic bump keeps the
	// version distinct so a cached client notices the replacement.
	if err := store.SetResourcePdfKey(ctx, resource.ID, "resources/res-1/original-2.pdf", now); err != nil {
		t.Fatal(err)
	}
	second, err := store.CourseContentRevision(ctx, "C1")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("course revision did not advance: %q", first)
	}
	if _, err := a.GetConfigVersions(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCourseResourceListIsNotTruncatedAtOnePage(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := store.CreateCourse(ctx, sqlite.CourseInput{Code: "C1", Name: "高等数学"}, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 205; i++ {
		if _, err := store.CreateResource(ctx, sqlite.ResourceInput{
			Name: fmt.Sprintf("试卷%03d.pdf", i), CourseCode: "C1",
		}, now); err != nil {
			t.Fatal(err)
		}
	}
	items, total, _, err := a.ListCourseResourcesForUser(ctx, "C1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 205 || len(items) != 205 {
		t.Fatalf("resources = %d, total = %d, want 205", len(items), total)
	}
}

func TestResourceAIVisibility(t *testing.T) {
	a, store := newCourseApp(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := store.CreateCourse(ctx, sqlite.CourseInput{Code: "C1", Name: "高等数学"}, now); err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateResource(ctx, sqlite.ResourceInput{Name: "期末试卷.pdf", CourseCode: "C1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourcePdfKey(ctx, resource.ID, "resources/res-1/original.pdf", now); err != nil {
		t.Fatal(err)
	}

	task, _, err := store.CreateAITask(ctx, resource.ID, "basic", "admin")
	if err != nil {
		t.Fatal(err)
	}
	status := "succeeded"
	markdown := "# 解析结果"
	outputKey := "ai/papers/" + resource.ID + "/" + task.ID + ".pdf"
	outputSize := int64(1234)
	totalPages := 3
	solvable := 1
	if err := store.UpdateAITask(ctx, task.ID, sqlite.AITaskPatch{
		Status: &status, Markdown: &markdown, OutputKey: &outputKey,
		OutputSize: &outputSize, TotalPages: &totalPages, Solvable: &solvable,
	}); err != nil {
		t.Fatal(err)
	}

	views, _, version, err := a.ListCourseResourcesForUser(ctx, "C1")
	if err != nil || len(views) != 1 {
		t.Fatalf("views = %#v, err = %v", views, err)
	}
	if !views[0].HasAI || views[0].AIUpdatedAt == "" || len(views[0].AIModes) != 1 || views[0].AIModes[0] != "basic" {
		t.Fatalf("resource AI fields = %#v", views[0])
	}

	courses, err := a.ListResourceCourses(ctx)
	if err != nil || len(courses) != 1 {
		t.Fatalf("courses = %#v, err = %v", courses, err)
	}
	if courses[0].Version != version {
		t.Fatalf("course version = %q, resource version = %q", courses[0].Version, version)
	}

	ai, err := a.ResourceAIForUser(ctx, resource.ID, "basic", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ai.Available || ai.Markdown != markdown || ai.TotalPages != totalPages {
		t.Fatalf("ai = %#v", ai)
	}
}
