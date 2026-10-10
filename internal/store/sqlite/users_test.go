package sqlite

import (
	"reflect"
	"testing"
)

func TestUserListWhere(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		search    string
		empty     string
		wantWhere string
		wantArgs  []any
	}{
		{name: "no filters", wantWhere: "", wantArgs: []any{}},
		{
			name:      "status only",
			status:    " active ",
			wantWhere: " WHERE u.status = ?",
			wantArgs:  []any{"active"},
		},
		{
			name:      "empty class",
			empty:     "class",
			wantWhere: " WHERE u.class_name = ''",
			wantArgs:  []any{},
		},
		{
			name:      "search only",
			search:    " 计算机 ",
			wantWhere: " WHERE (u.display_name LIKE ? ESCAPE '\\' OR u.college_name LIKE ? ESCAPE '\\' OR u.class_name LIKE ? ESCAPE '\\' OR " + latestUserAppVersionSQL + " LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"%计算机%", "%计算机%", "%计算机%", "%计算机%"},
		},
		{
			name:      "status and search",
			status:    "active",
			search:    "25信计2",
			wantWhere: " WHERE u.status = ? AND (u.display_name LIKE ? ESCAPE '\\' OR u.college_name LIKE ? ESCAPE '\\' OR u.class_name LIKE ? ESCAPE '\\' OR " + latestUserAppVersionSQL + " LIKE ? ESCAPE '\\')",
			wantArgs:  []any{"active", "%25信计2%", "%25信计2%", "%25信计2%", "%25信计2%"},
		},
		{
			name:      "wildcards are escaped",
			search:    `100%_a\b`,
			wantWhere: " WHERE (u.display_name LIKE ? ESCAPE '\\' OR u.college_name LIKE ? ESCAPE '\\' OR u.class_name LIKE ? ESCAPE '\\' OR " + latestUserAppVersionSQL + " LIKE ? ESCAPE '\\')",
			wantArgs:  []any{`%100\%\_a\\b%`, `%100\%\_a\\b%`, `%100\%\_a\\b%`, `%100\%\_a\\b%`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args := userListWhere(tc.status, tc.search, tc.empty)
			if where != tc.wantWhere {
				t.Fatalf("where = %q, want %q", where, tc.wantWhere)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, tc.wantArgs)
			}
		})
	}
}
