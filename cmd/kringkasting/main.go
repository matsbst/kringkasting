// Command kringkasting serves NRK podcasts as open RSS feeds.
//
//	kringkasting              run the server
//	kringkasting healthcheck  exit 0 when the local server answers (Docker HEALTHCHECK)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/matsbst/kringkasting/internal/app"
	"github.com/matsbst/kringkasting/internal/config"
)

// shutdownGrace is how long in-flight requests and the current crawl page
// get to finish on SIGTERM; crawl checkpoints are saved per page.
const shutdownGrace = 10 * time.Second

func main() {
	cfg := config.FromEnv()
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(cfg))
	}
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func healthcheck(cfg config.Config) int {
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		port = "8000"
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/personvern")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "status", resp.StatusCode)
		return 1
	}
	return 0
}

func run(cfg config.Config) error {
	// background work belongs to the process, never to a request
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	application, err := app.New(ctx, cfg, app.Options{})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           application.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Kringkasting listening on %s", cfg.Addr)
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Print("Shutting down")
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	// ctx is cancelled, so the crawler stops after its current page and
	// its checkpoint; then the database closes
	return application.Close(shutdownCtx)
}
