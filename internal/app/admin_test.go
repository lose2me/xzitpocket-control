package app

import "testing"

func TestNormalizeUserListSort(t *testing.T) {
	cases := []struct {
		sort, order         string
		wantSort, wantOrder string
		wantErr             bool
	}{
		{sort: "", order: "", wantSort: "last_login_at", wantOrder: "desc"},
		{sort: " seq ", order: " ASC ", wantSort: "seq", wantOrder: "asc"},
		{sort: "created_at", order: "desc", wantSort: "created_at", wantOrder: "desc"},
		{sort: "device_count", order: "asc", wantSort: "device_count", wantOrder: "asc"},
		{sort: "seq", order: "", wantSort: "seq", wantOrder: "desc"},
		{sort: "nickname", order: "asc", wantErr: true},
		{sort: "seq", order: "sideways", wantErr: true},
		{sort: "seq; DROP TABLE users", order: "asc", wantErr: true},
	}
	for _, tc := range cases {
		sort, order, err := normalizeUserListSort(tc.sort, tc.order)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("normalizeUserListSort(%q, %q) expected error", tc.sort, tc.order)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeUserListSort(%q, %q) error: %v", tc.sort, tc.order, err)
		}
		if sort != tc.wantSort || order != tc.wantOrder {
			t.Fatalf("normalizeUserListSort(%q, %q) = (%q, %q), want (%q, %q)", tc.sort, tc.order, sort, order, tc.wantSort, tc.wantOrder)
		}
	}
}
