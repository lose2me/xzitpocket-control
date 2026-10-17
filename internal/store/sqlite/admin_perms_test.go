package sqlite

import "testing"

func TestAdminAllows(t *testing.T) {
	admin := Admin{Permissions: "overview=r,users=rw"}
	cases := []struct {
		page  string
		write bool
		want  bool
	}{
		{"overview", false, true},
		{"overview", true, false},
		{"users", false, true},
		{"users", true, true},
		{"audit", false, false},
		{"", true, true},
	}
	for _, tc := range cases {
		if got := admin.Allows(tc.page, tc.write); got != tc.want {
			t.Fatalf("Allows(%q,%v) = %v, want %v", tc.page, tc.write, got, tc.want)
		}
	}
	super := Admin{Permissions: "*=rw"}
	if !super.Allows("config", true) || !super.Allows("audit", false) {
		t.Fatal("wildcard admin should allow everything")
	}
	empty := Admin{}
	if empty.Allows("overview", false) {
		t.Fatal("empty permissions should deny")
	}
}
