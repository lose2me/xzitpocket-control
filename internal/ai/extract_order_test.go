package ai

import "testing"

func TestOrderPageItemsTwoColumns(t *testing.T) {
	items := []pageItem{
		{top: 10, left: 320, text: "R1"},
		{top: 10, left: 60, text: "L1"},
		{top: 20, left: 320, text: "R2"},
		{top: 20, left: 60, text: "L2"},
		{top: 30, left: 320, text: "R3"},
		{top: 30, left: 60, text: "L3"},
	}
	orderPageItems(items, 600)
	want := []string{"L1", "L2", "L3", "R1", "R2", "R3"}
	for i, item := range items {
		if item.text != want[i] {
			t.Fatalf("order = %v, want %v", texts(items), want)
		}
	}
}

func TestOrderPageItemsSingleColumn(t *testing.T) {
	items := []pageItem{
		{top: 20, left: 60, text: "第二行"},
		{top: 10, left: 60, text: "第一行"},
		{top: 30, left: 90, text: "第三行"},
	}
	orderPageItems(items, 600)
	want := []string{"第一行", "第二行", "第三行"}
	for i, item := range items {
		if item.text != want[i] {
			t.Fatalf("order = %v, want %v", texts(items), want)
		}
	}
}

func texts(items []pageItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.text)
	}
	return out
}
