package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"xzitpocket-control/internal/app"
	"xzitpocket-control/internal/config"
	"xzitpocket-control/internal/httpapi"
	"xzitpocket-control/internal/store/sqlite"
	adminweb "xzitpocket-control/web"
)

func staticAssets(cfg config.Config, logger *slog.Logger) fs.FS {
	if cfg.WebDir != "" {
		return os.DirFS(cfg.WebDir)
	}
	staticFS, err := fs.Sub(adminweb.Files, ".")
	if err != nil {
		logger.Error("load web assets", "error", err)
		os.Exit(1)
	}
	return staticFS
}

// spaHandler serves the console as a single-page app: paths that do not match a
// shipped asset (for example /users) fall back to index.html so a browser
// reload keeps the current page instead of 404ing. Assets keep their own 404 so
// a broken script reference stays visible.
func spaHandler(staticFS fs.FS) http.Handler {
	files := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !isAppRoute(staticFS, r.URL.Path) {
			files.ServeHTTP(w, r)
			return
		}
		// Serve the bytes directly: handing /index.html to http.FileServer would
		// redirect the client to "./" instead of answering the route.
		data, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}
	})
}

func isAppRoute(staticFS fs.FS, urlPath string) bool {
	if strings.HasPrefix(urlPath, "/api") {
		return false
	}
	name := path.Clean(strings.TrimPrefix(urlPath, "/"))
	if name == "" || name == "." || path.Ext(name) != "" {
		return false
	}
	_, err := fs.Stat(staticFS, name)
	return err != nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	store, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := store.Migrate(ctx); err != nil {
		cancel()
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}
	cancel()
	application, err := app.New(cfg, store, logger)
	if err != nil {
		logger.Error("initialize application", "error", err)
		os.Exit(1)
	}
	staticFS := staticAssets(cfg, logger)
	static := spaHandler(staticFS)
	handler := httpapi.New(application, static, logger)
	server := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				application.CleanupTelemetry(cleanupCtx)
			case <-cleanupCtx.Done():
				return
			}
		}
	}()
	go func() {
		logger.Info("control server listening", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown", "error", err)
	}
}
