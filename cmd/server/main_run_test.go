package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestServerStartsServesAndShutsDown runs the real binary so the startup,
// worker goroutines, signal handling and graceful shutdown are exercised.
func TestServerStartsServesAndShutsDown(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the server binary")
	}
	if runtime.GOOS == "windows" {
		t.Skip("signal based shutdown is not portable to Windows")
	}

	dir := t.TempDir()
	binary := filepath.Join(dir, "control-server")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := "CONTROL_ADDR=" + addr + "\n" +
		"CONTROL_DB=" + filepath.Join(dir, "data", "control.db") + "\n" +
		"CONTROL_PUBLIC_BASE_URL=https://con.example.test\n" +
		"CONTROL_ADMIN_KEY=test-password\n" +
		"CONTROL_TOKEN_PEPPER=YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE\n" +
		"CONTROL_ID_PEPPER=YmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmI\n" +
		"CONTROL_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY\n"
	if err := os.WriteFile(filepath.Join(dir, "data", ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binary)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CONTROL_AI_WORK_DIR="+filepath.Join(dir, "ai-work"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	go func() {
		buffer := make([]byte, 4096)
		for {
			if _, err := stdout.Read(buffer); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

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
			t.Fatalf("server did not become healthy: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	resp, err := http.Get(base + "/assets/missing.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset = %d", resp.StatusCode)
	}
	// The SPA route falls back to index.html.
	resp, err = http.Get(base + "/users")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spa route = %d", resp.StatusCode)
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("server exited with %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down after SIGINT")
	}
	fmt.Fprintln(os.Stderr, "control server started, served and stopped cleanly")
}
