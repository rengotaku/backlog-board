package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lmittmann/tint"

	"github.com/rengotaku/backlog-board/internal/backlog"
	"github.com/rengotaku/backlog-board/internal/config"
	"github.com/rengotaku/backlog-board/internal/handler"
	"github.com/rengotaku/backlog-board/internal/store"
	"github.com/rengotaku/backlog-board/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var logLevel slog.LevelVar
	if l := os.Getenv("LOG_LEVEL"); l != "" {
		_ = logLevel.UnmarshalText([]byte(l))
	}
	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:      &logLevel,
		TimeFormat: time.Kitchen,
	})))

	cfg, err := config.Load(os.Getenv("BACKLOG_BOARD_CONFIG"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	apiKey := os.Getenv("BACKLOG_API_KEY")

	templates, err := loadTemplates()
	if err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	staticFiles, err := fs.Sub(web.FS, "static")
	if err != nil {
		return fmt.Errorf("load static: %w", err)
	}

	cache := store.New(cfg.CachePath)

	var refreshFn func() error
	if apiKey != "" {
		blClient := backlog.NewClient(cfg.Domain, apiKey)
		refreshFn = func() error {
			// 直前の snapshot を comments cache 流用のために読み込む（無ければ nil で続行）。
			prev, err := cache.Load()
			if err != nil {
				prev = nil
			}
			snap, err := backlog.Fetch(blClient, backlog.FetchOptions{Count: 100, IncludeRead: true}, prev)
			if err != nil {
				return err
			}
			return cache.Save(snap)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if refreshFn != nil {
		go func() {
			if err := refreshFn(); err != nil {
				slog.Warn("initial fetch failed", "error", err)
			}
			ticker := time.NewTicker(cfg.FetchInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := refreshFn(); err != nil {
						slog.Warn("periodic fetch failed", "error", err)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	h := handler.New(cache, templates, staticFiles, refreshFn)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      h.Routes(),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("starting server", "port", cfg.Port, "cache", cfg.CachePath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancel()

	slog.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	slog.Info("server stopped")
	return nil
}

func loadTemplates() (map[string]*template.Template, error) {
	pages := []struct{ name, path string }{
		{"index.html", "templates/index.html"},
	}
	m := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t, err := template.ParseFS(web.FS, "templates/base.html", p.path)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p.name, err)
		}
		m[p.name] = t
	}
	return m, nil
}
