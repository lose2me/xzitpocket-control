package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/store/sqlite"
)

func newAdminTestApp(t *testing.T) (*App, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{
		TokenPepper:   []byte("token"),
		IDPepper:      []byte("id"),
		EncryptionKey: []byte("01234567890123456789012345678901"),
		AdminKey:      "super-key",
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return a, store
}

func TestAdminLoginMultiAccountPermissions(t *testing.T) {
	a, _ := newAdminTestApp(t)
	ctx := context.Background()

	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{
		Name: "viewer", Key: "viewer-key", Permissions: "overview=r,users=rw",
	}, "admin"); err != nil {
		t.Fatal(err)
	}

	_, viewer, err := a.AdminLogin(ctx, "viewer-key")
	if err != nil {
		t.Fatal(err)
	}
	if !viewer.Admin.Allows("overview", false) || viewer.Admin.Allows("overview", true) {
		t.Fatalf("viewer overview permissions wrong: %q", viewer.Admin.Permissions)
	}
	if !viewer.Admin.Allows("users", true) || viewer.Admin.Allows("audit", false) {
		t.Fatalf("viewer page permissions wrong: %q", viewer.Admin.Permissions)
	}

	_, super, err := a.AdminLogin(ctx, "super-key")
	if err != nil {
		t.Fatal(err)
	}
	if !super.Admin.Allows("audit", true) || !super.Admin.Allows("config", false) {
		t.Fatalf("super permissions wrong: %q", super.Admin.Permissions)
	}

	if _, _, err := a.AdminLogin(ctx, "wrong-key"); err == nil {
		t.Fatal("wrong key should fail")
	}
}

func TestFreshDatabaseHasOnlySuperAdmin(t *testing.T) {
	a, store := newAdminTestApp(t)
	ctx := context.Background()

	if _, _, err := a.AdminLogin(ctx, "super-key"); err != nil {
		t.Fatal(err)
	}
	admins, err := store.ListAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 1 {
		t.Fatalf("admins = %d, want only the super admin", len(admins))
	}
}
