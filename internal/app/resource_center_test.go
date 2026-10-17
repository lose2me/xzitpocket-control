package app

import (
	"context"
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
	if len(courses) != 1 {
		t.Fatalf("courses = %#v, want only the course with content", courses)
	}
	got := courses[0]
	if got.Code != "C1" || got.QuestionBankCount != 1 || got.ResourceCount != 1 || got.TextbookCount != 1 {
		t.Fatalf("course = %#v", got)
	}

	resources, total, err := a.ListCourseResourcesForUser(ctx, "C1")
	if err != nil || total != 1 || len(resources) != 1 || resources[0].Name != "期末试卷.pdf" {
		t.Fatalf("resources = %#v, total = %d, err = %v", resources, total, err)
	}
	textbooks, total, err := a.ListCourseTextbooksForUser(ctx, "C1")
	if err != nil || total != 1 || len(textbooks) != 1 || textbooks[0].Name != "高等数学（上）" {
		t.Fatalf("textbooks = %#v, total = %d, err = %v", textbooks, total, err)
	}
	if _, _, err := a.ListCourseResourcesForUser(ctx, ""); err == nil {
		t.Fatal("empty course code should be rejected")
	}
}
