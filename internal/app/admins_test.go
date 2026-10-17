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

func TestAdminManagement(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
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
	ctx := context.Background()

	created, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "viewer2", Key: "viewer-key-2", Permissions: "overview=r,users=rw"}, "admin")
	if err != nil || created.ID == "" || created.Super {
		t.Fatalf("create = %#v, err = %v", created, err)
	}
	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "viewer2", Key: "another-key", Permissions: "overview=r"}, "admin"); err == nil {
		t.Fatal("duplicate admin should fail")
	}
	nameless, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Key: "nameless-key", Permissions: "audit=r"}, "admin")
	if err != nil || !adminNamePattern.MatchString(nameless.Name) {
		t.Fatalf("nameless create = %#v, err = %v", nameless, err)
	}
	if _, _, err := a.AdminLogin(ctx, "nameless-key"); err != nil {
		t.Fatalf("nameless admin should log in: %v", err)
	}
	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "bad", Key: "bad-key-123", Permissions: "*"}, "admin"); err == nil {
		t.Fatal("wildcard admin should be rejected")
	}

	_, principal, err := a.AdminLogin(ctx, "viewer-key-2")
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Admin.Allows("users", true) || principal.Admin.Allows("audit", false) {
		t.Fatalf("permissions = %q", principal.Admin.Permissions)
	}

	updated, err := a.UpdateAdminForAdmin(ctx, created.ID, AdminWriteInput{Permissions: "audit=r"}, "admin")
	if err != nil || updated.Permissions != "audit=r" {
		t.Fatalf("update = %#v, err = %v", updated, err)
	}
	_, principal, err = a.AdminLogin(ctx, "viewer-key-2")
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Admin.Allows("audit", false) || principal.Admin.Allows("users", false) {
		t.Fatalf("updated permissions = %q", principal.Admin.Permissions)
	}

	if err := a.DeleteAdminForAdmin(ctx, created.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.AdminLogin(ctx, "viewer-key-2"); err == nil {
		t.Fatal("deleted admin should not log in")
	}

	super, err := a.Store.GetAdminByName(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateAdminForAdmin(ctx, super.ID, AdminWriteInput{Permissions: "audit=r"}, "admin"); err == nil {
		t.Fatal("super admin should not be editable")
	}
	if err := a.DeleteAdminForAdmin(ctx, super.ID, "admin"); err == nil {
		t.Fatal("super admin should not be deletable")
	}
	if _, err := a.CreateAdminForAdmin(ctx, AdminWriteInput{Name: "admin", Key: "x-key-123", Permissions: "audit=r"}, "admin"); err == nil {
		t.Fatal("creating a second super admin should fail")
	}
}
