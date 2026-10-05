package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newUserListStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func insertUser(t *testing.T, store *Store, id, name, college, class string, createdAt time.Time) {
	t.Helper()
	_, err := store.DB.ExecContext(context.Background(),
		"INSERT INTO users(id, status, display_name, college_name, class_name, created_at, last_login_at) VALUES (?, 'active', ?, ?, ?, ?, ?)",
		id, name, college, class, createdAt.UnixMilli(), createdAt.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
}

// Seq numbers users by creation order over the whole table, so filtering the
// list must not renumber the remaining rows.
func TestListUsersSeqFollowsGlobalCreationOrder(t *testing.T) {
	store := newUserListStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	insertUser(t, store, "usr_a", "最早", "", "", base)
	insertUser(t, store, "usr_b", "中间", "数学与统计学院", "", base.Add(time.Hour))
	insertUser(t, store, "usr_c", "最新", "", "25信计2", base.Add(2*time.Hour))

	items, err := store.ListUsers(ctx, 50, 0, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	total, err := store.CountUsers(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("total = %d, items = %d", total, len(items))
	}
	want := map[string]int{"usr_a": 1, "usr_b": 2, "usr_c": 3}
	for _, item := range items {
		if item.Seq != want[item.ID] {
			t.Fatalf("user %s seq = %d, want %d", item.ID, item.Seq, want[item.ID])
		}
	}

	// Same created_at must still get a stable, distinct order.
	same := base.Add(3 * time.Hour)
	insertUser(t, store, "usr_d", "同刻1", "", "", same)
	insertUser(t, store, "usr_e", "同刻2", "", "", same)
	items, err = store.ListUsers(ctx, 50, 0, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, item := range items {
		if seen[item.Seq] {
			t.Fatalf("duplicate seq %d", item.Seq)
		}
		seen[item.Seq] = true
	}

	// A filtered search keeps the global numbers.
	items, err = store.ListUsers(ctx, 50, 0, "active", "最新", "", "")
	if err != nil {
		t.Fatal(err)
	}
	total, err = store.CountUsers(ctx, "active", "最新")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("filtered total = %d, items = %d", total, len(items))
	}
	if items[0].ID != "usr_c" || items[0].Seq != 3 {
		t.Fatalf("filtered user = %s seq %d, want usr_c seq 3", items[0].ID, items[0].Seq)
	}
}

func TestUserListOrderBy(t *testing.T) {
	cases := []struct {
		sort  string
		order string
		want  string
	}{
		{sort: "seq", order: "asc", want: "r.seq ASC, u.id"},
		{sort: "seq", order: "DESC", want: "r.seq DESC, u.id"},
		{sort: "created_at", order: "asc", want: "u.created_at ASC, u.id"},
		{sort: "last_login_at", order: "desc", want: "u.last_login_at DESC, u.id"},
		{sort: "device_count", order: "desc", want: "device_count DESC, u.id"},
		{sort: "", order: "", want: "u.last_login_at DESC, u.id"},
		{sort: "unknown", order: "asc", want: "u.last_login_at DESC, u.id"},
		{sort: "seq; DROP TABLE users", order: "asc", want: "u.last_login_at DESC, u.id"},
		{sort: "seq", order: "asc; DROP TABLE users", want: "r.seq DESC, u.id"},
	}
	for _, tc := range cases {
		if got := userListOrderBy(tc.sort, tc.order); got != tc.want {
			t.Fatalf("userListOrderBy(%q, %q) = %q, want %q", tc.sort, tc.order, got, tc.want)
		}
	}
}

func TestValidUserListSort(t *testing.T) {
	for _, key := range []string{"seq", "created_at", "last_login_at", "display_name", "college_name", "class_name", "status", "device_count", "app_version"} {
		if !ValidUserListSort(key) {
			t.Fatalf("%s should be sortable", key)
		}
	}
	for _, key := range []string{"", "id", "actions", "1", "seq ASC"} {
		if ValidUserListSort(key) {
			t.Fatalf("%q should not be sortable", key)
		}
	}
}

func TestListUsersSortsAndPaginates(t *testing.T) {
	store := newUserListStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	insertUser(t, store, "usr_1", "一", "", "", base)
	insertUser(t, store, "usr_2", "二", "", "", base.Add(time.Hour))
	insertUser(t, store, "usr_3", "三", "", "", base.Add(2*time.Hour))

	// 默认：最近登录倒序（三人相同，稳定按 id 兜底）
	items, err := store.ListUsers(ctx, 10, 0, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d", len(items))
	}

	// 按注册序号升序
	items, err = store.ListUsers(ctx, 10, 0, "", "", "seq", "asc")
	if err != nil {
		t.Fatal(err)
	}
	for index, item := range items {
		if item.Seq != index+1 {
			t.Fatalf("seq order = %d at %d", item.Seq, index)
		}
	}

	// 按注册序号降序 + 分页：第 2 页每页 1 条 → 序号 2
	items, err = store.ListUsers(ctx, 1, 1, "", "", "seq", "desc")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "usr_2" || items[0].Seq != 2 {
		t.Fatalf("paged items = %+v", items)
	}

	// 按注册时间升序
	items, err = store.ListUsers(ctx, 10, 0, "", "", "created_at", "asc")
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ID != "usr_1" || items[2].ID != "usr_3" {
		t.Fatalf("created_at order = %s..%s", items[0].ID, items[2].ID)
	}
}

// Every whitelisted sort must be accepted by the real query (alias and
// ambiguity mistakes only show up when SQLite parses it).
func TestListUsersAcceptsEverySortKey(t *testing.T) {
	store := newUserListStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	insertUser(t, store, "usr_1", "甲", "数学与统计学院", "25信计2", base)
	insertUser(t, store, "usr_2", "乙", "", "", base.Add(time.Hour))
	for key := range userListSorts {
		for _, order := range []string{"asc", "desc"} {
			if _, err := store.ListUsers(ctx, 10, 0, "", "", key, order); err != nil {
				t.Fatalf("sort %s %s: %v", key, order, err)
			}
			if _, err := store.CountUsers(ctx, "", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
}
