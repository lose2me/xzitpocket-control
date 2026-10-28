package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunServesAndStopsWhenContextEnds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	if err := os.MkdirAll("data", 0o700); err != nil {
		t.Fatal(err)
	}
	env := "CONTROL_ADDR=" + addr + "\n" +
		"CONTROL_DB=" + filepath.Join(dir, "data", "control.db") + "\n" +
		"CONTROL_WEB_DIR=" + filepath.Join(dir, "web") + "\n" +
		"CONTROL_ADMIN_KEY=test-password\n" +
		"CONTROL_TOKEN_PEPPER=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE\n" +
		"CONTROL_ID_PEPPER=YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmI\n" +
		"CONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n"
	if err := os.WriteFile(filepath.Join("data", ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("web", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("web", "index.html"), []byte("<html>console</html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	base := "http://" + addr
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not become healthy: %v", err)
		}
		select {
		case err := <-done:
			t.Fatalf("run exited early: %v", err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}

	for path, want := range map[string]int{
		"/users":             http.StatusOK,
		"/assets/missing.js": http.StatusNotFound,
	} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop after the context ended")
	}
}

func TestRunFailsOnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("data", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", ".env"), []byte("CONTROL_TOKEN_PEPPER=c2hvcnQ\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("invalid config should fail")
	}
}

func TestRunFailsWhenAddressIsInUse(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	addr := listener.Addr().String()

	if err := os.MkdirAll("data", 0o700); err != nil {
		t.Fatal(err)
	}
	env := "CONTROL_ADDR=" + addr + "\n" +
		"CONTROL_DB=" + filepath.Join(dir, "data", "control.db") + "\n" +
		"CONTROL_ADMIN_KEY=test-password\n" +
		"CONTROL_TOKEN_PEPPER=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE\n" +
		"CONTROL_ID_PEPPER=YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmI\n" +
		"CONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n"
	if err := os.WriteFile(filepath.Join("data", ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	err = run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("address in use should be reported")
	}
}
