package main

import (
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"xzitpocket-control/internal/config"
)

func mimeTypeOfMJS() string {
	if ext, err := mime.ExtensionsByType("text/javascript"); err == nil {
		for _, candidate := range ext {
			if candidate == ".mjs" {
				return "text/javascript; charset=utf-8"
			}
		}
	}
	return mime.TypeByExtension(".mjs")
}

func fsRead(files fs.FS, name string) ([]byte, error) { return fs.ReadFile(files, name) }

func fsStat(files fs.FS, name string) (fs.FileInfo, error) { return fs.Stat(files, name) }

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestInitRegistersModuleMIME(t *testing.T) {
	if got := mimeTypeOfMJS(); got != "text/javascript; charset=utf-8" {
		t.Fatalf("mjs mime = %q", got)
	}
}

func TestStaticAssetsUsesWebDirOrEmbeddedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>console</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := staticAssets(config.Config{WebDir: dir}, testLogger())
	raw, err := fsRead(files, "index.html")
	if err != nil || string(raw) != "<html>console</html>" {
		t.Fatalf("web dir assets = %q, %v", raw, err)
	}
	embedded := staticAssets(config.Config{}, testLogger())
	if _, err := fsStat(embedded, "."); err != nil {
		t.Fatalf("embedded assets = %v", err)
	}
}

func TestSPAHandlerFallsBackToIndexForRoutes(t *testing.T) {
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<html>console</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	handler := spaHandler(static)

	cases := []struct {
		method, path string
		status       int
		body         string
		contentType  string
	}{
		{http.MethodGet, "/users", http.StatusOK, "<html>console</html>", "text/html; charset=utf-8"},
		{http.MethodHead, "/users", http.StatusOK, "", "text/html; charset=utf-8"},
		{http.MethodPost, "/users", http.StatusNotFound, "", ""},
		{http.MethodGet, "/assets/app.js", http.StatusOK, "console.log(1)", "text/javascript"},
		{http.MethodGet, "/missing.js", http.StatusNotFound, "", ""},
		{http.MethodGet, "/api/v1/users", http.StatusNotFound, "", ""},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		handler.ServeHTTP(recorder, req)
		if recorder.Code != tc.status {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, recorder.Code, tc.status)
		}
		if tc.body != "" && recorder.Body.String() != tc.body {
			t.Fatalf("%s %s body = %q", tc.method, tc.path, recorder.Body.String())
		}
		if tc.contentType != "" && !strings.HasPrefix(recorder.Header().Get("Content-Type"), tc.contentType) {
			t.Fatalf("%s %s content type = %q", tc.method, tc.path, recorder.Header().Get("Content-Type"))
		}
	}
}

func TestSPAHandlerWithoutIndexFallsBack(t *testing.T) {
	static := fstest.MapFS{"assets/app.js": {Data: []byte("x")}}
	recorder := httptest.NewRecorder()
	spaHandler(static).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/users", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing index status = %d", recorder.Code)
	}
}

func TestIsAppRoute(t *testing.T) {
	static := fstest.MapFS{
		"index.html": {Data: []byte("x")},
		"app.js":     {Data: []byte("x")},
		"console":    {Data: []byte("x")},
	}
	cases := map[string]bool{
		"/":             false,
		"/users":        true,
		"/users/":       true,
		"/app.js":       false,
		"/console":      false,
		"/api":          false,
		"/api/v1/users": false,
		"/nope.png":     false,
	}
	for path, want := range cases {
		if got := isAppRoute(static, path); got != want {
			t.Fatalf("isAppRoute(%q) = %v, want %v", path, got, want)
		}
	}
}
