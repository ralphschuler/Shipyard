package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"taskboard/internal/automation"
	"taskboard/internal/mcp"
	"taskboard/internal/store"
	"taskboard/internal/updates"
	"taskboard/internal/web"
	"time"
)

const (
	defaultAddress     = "127.0.0.1:8080"
	defaultDatabaseURL = "postgres://taskboard:taskboard@localhost:5432/taskboard?sslmode=disable"
	shutdownTimeout    = 10 * time.Second
)

func isStreamingPath(path string) bool {
	return path == "/events" || path == "/mcp"
}

// requestTimeout protects ordinary requests without severing the long-lived
// Server-Sent Events and MCP transports.
func requestTimeout(next http.Handler) http.Handler {
	standard := http.TimeoutHandler(next, 30*time.Second, "request timed out\n")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStreamingPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		standard.ServeHTTP(w, r)
	})
}

// These values are replaced by the release workflow with -ldflags. Keeping
// development defaults makes local `go run` useful while still exposing an
// immutable build identity to the Updates view in production.
var (
	version = "development"
	commit  = "unknown"
	builtAt string
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "--validate-embedded-app" {
		if err := web.ValidateEmbeddedApp(os.Args[2], os.Args[3]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 8 && os.Args[1] == "--monitor-restart" {
		if err := updates.RunRestartMonitor(os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6], os.Args[7]); err != nil {
			log.Print(err)
			os.Exit(75)
		}
		return
	}
	setBuildMetadata()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseURL := envOrDefault("DATABASE_URL", defaultDatabaseURL)
	address := envOrDefault("TASKBOARD_ADDR", defaultAddress)

	openContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	s, err := store.Open(openContext, databaseURL)
	cancel()
	if err != nil {
		log.Fatalf("Datenbank konnte nicht geöffnet werden: %v", err)
	}
	defer s.DB.Close()

	if err := s.Migrate(ctx); err != nil {
		log.Fatalf("Datenbankmigration fehlgeschlagen: %v", err)
	}

	worker := &automation.Worker{Store: s}
	worker.Start(ctx)

	app := web.NewWithUpdateOrchestrator(s, worker, updates.NewProductionOrchestrator(s))
	mux := http.NewServeMux()
	app.Register(mux)
	// MCP has its own bearer-token authentication and is intentionally outside
	// the browser session middleware.
	mux.Handle("/mcp", mcp.New(s, worker))
	server := &http.Server{
		Addr:              address,
		Handler:           requestTimeout(app.Protected(app.HTMX(mux))),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.ListenAndServe() }()

	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP-Server konnte nicht gestartet werden: %v", err)
		}
	case <-ctx.Done():
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("HTTP-Server konnte nicht sauber beendet werden: %v", err)
		}
	}
}

func setBuildMetadata() {
	setDefaultEnv("TASKBOARD_VERSION", version)
	setDefaultEnv("TASKBOARD_COMMIT_SHA", commit)
	setDefaultEnv("TASKBOARD_BUILD_TIME", builtAt)
}

func setDefaultEnv(key, value string) {
	if value != "" && os.Getenv(key) == "" {
		_ = os.Setenv(key, value)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
