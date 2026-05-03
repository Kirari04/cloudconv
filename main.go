package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kirari04/cloudconv/internal/auth"
	"github.com/kirari04/cloudconv/internal/config"
	apphttp "github.com/kirari04/cloudconv/internal/http"
	"github.com/kirari04/cloudconv/internal/jobs"
	"github.com/kirari04/cloudconv/internal/store"
	"github.com/kirari04/cloudconv/internal/uploads"
)

func main() {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.UploadDir, 0755); err != nil {
		log.Fatalf("failed to create upload directory: %v", err)
	}
	if err := os.MkdirAll(cfg.ConvertedDir, 0755); err != nil {
		log.Fatalf("failed to create converted directory: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer st.Close()

	authSvc := auth.New(st, cfg.CookieSecure)
	uploadSvc := uploads.New(cfg, st)
	jobSvc := jobs.New(cfg, st)
	jobSvc.Start(ctx)
	server := apphttp.New(cfg, st, authSvc, uploadSvc, jobSvc)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		log.Printf("CloudConv listening on %s", cfg.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown failed: %v", err)
	}
}
