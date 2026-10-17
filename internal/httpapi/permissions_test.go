package httpapi

import (
	"net/http"
	"testing"
)

func TestAdminPageFor(t *testing.T) {
	cases := []struct {
		path, method string
		page         string
		write        bool
	}{
		{"/admin/textbooks", http.MethodGet, "textbooks", false},
		{"/admin/textbooks/123/pdf", http.MethodPost, "textbooks", true},
		{"/admin/cdn/stats", http.MethodGet, "overview", false},
		{"/admin/overview-display", http.MethodPut, "overview", true},
		{"/admin/app/release-test", http.MethodPut, "config", true},
		{"/admin/share-codes/abc", http.MethodDelete, "share-codes", true},
		{"/admin/me", http.MethodGet, "", false},
		{"/admin/admins", http.MethodGet, "admins", false},
		{"/admin/admins/abc", http.MethodDelete, "admins", true},
	}
	for _, tc := range cases {
		page, write := adminPageFor(tc.path, tc.method)
		if page != tc.page || write != tc.write {
			t.Fatalf("adminPageFor(%q,%q) = (%q,%v), want (%q,%v)", tc.path, tc.method, page, write, tc.page, tc.write)
		}
	}
}
