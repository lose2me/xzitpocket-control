package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func indexNames(t *testing.T, store *Store) map[string]bool {
	t.Helper()
	rows, err := store.DB.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'index'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

// TestSchemaIndexes covers the declared indexes: the two added for the hot
// device-revocation and login-attempt queries, and the obsolete archived_at
// indexes that must disappear from existing databases on open.
func TestSchemaIndexes(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Simulate a database created before the archived_at indexes were removed.
	for name, ddl := range map[string]string{
		"idx_question_banks_archived": `CREATE INDEX idx_question_banks_archived ON question_banks(archived_at)`,
		"idx_textbooks_archived":      `CREATE INDEX idx_textbooks_archived ON textbooks(archived_at)`,
		"idx_resources_archived":      `CREATE INDEX idx_resources_archived ON resources(archived_at)`,
	} {
		if _, err := store.DB.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("create legacy index %s: %v", name, err)
		}
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	names := indexNames(t, store)
	for _, name := range []string{"idx_sessions_device", "idx_audit_action_time"} {
		if !names[name] {
			t.Fatalf("index %s missing", name)
		}
	}
	for _, name := range removedIndexes {
		if names[name] {
			t.Fatalf("obsolete index %s still present", name)
		}
	}

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`UPDATE sessions SET revoked_at = ? WHERE device_id = ? AND revoked_at IS NULL`, "idx_sessions_device"},
		{`SELECT COUNT(*) FROM audit_logs WHERE action = 'login_attempt' AND created_at >= ?`, "idx_audit_action_time"},
	} {
		rows, err := store.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+tc.query, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), tc.want) {
			t.Fatalf("query does not use %s:\n%s", tc.want, plan.String())
		}
	}
}
