package ai

import "testing"

func TestSplitReportsMarkers(t *testing.T) {
	basic, more, err := SplitReports("<!-- basic -->\n精简内容\n<!-- MORE -->\n小学生内容\n")
	if err != nil {
		t.Fatal(err)
	}
	if basic != "精简内容" || more != "小学生内容" {
		t.Fatalf("basic=%q more=%q", basic, more)
	}
}

func TestSplitReportsHeadingFallback(t *testing.T) {
	basic, more, err := SplitReports("## 精简版\n答案 A\n### 小学生版\n详解 B\n")
	if err != nil {
		t.Fatal(err)
	}
	if basic != "答案 A" || more != "详解 B" {
		t.Fatalf("basic=%q more=%q", basic, more)
	}
}

func TestSplitReportsRejectsMissingSection(t *testing.T) {
	if _, _, err := SplitReports("只有一段解析"); err == nil {
		t.Fatal("missing sections should fail")
	}
	if _, _, err := SplitReports("<!-- BASIC -->\n只有精简版"); err == nil {
		t.Fatal("missing more section should fail")
	}
}
