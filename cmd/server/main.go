// Package main is the entry point for nixpkgs-notifier server.
//
// It wires together all subparts in correct order.
// Most important ones are:
// configuration loading, database connection and migration,
// OIDC provider setup, session management, and background workers
// (dispatcher, checker, cleaner, branch fetcher).
//
// After all dependencies are initialized, it registers HTTP routes,
// chains middleware (security headers, request logger, session, CSRF),
// and starts HTTP(S) server.
//
// Graceful shutdown is handled via OS signal (SIGINT/SIGTERM):
// background workers are cancelled through shared context,
// and HTTP server is given 30 seconds to finish requests
// that are already being processed.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/app/packages"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/checker"
	"github.com/denyzzko/nixpkgs-notifier/internal/cleaner"
	"github.com/denyzzko/nixpkgs-notifier/internal/config"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/dispatcher"
	"github.com/denyzzko/nixpkgs-notifier/internal/middleware"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/web"
	"github.com/justinas/nosurf"
)

func main() {
	ctx := context.Background()

	// load configuration from env variables
	cfg, err := config.LoadEnvConfig()
	if err != nil {
		log.Fatalf("[ERROR] CONFIG: Could not load config from environment variables!: %v", err)
	}

	// check nix availability
	err = nix.CheckNixAvailability()
	if err != nil {
		log.Fatalf("[ERROR] NIX: Nix is not available on this system!: %v", err)
	}

	// open connection to db
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("[ERROR] DATABASE: Could not connect to database!: %v", err)
	}
	defer db.Close()
	log.Println("[INFO] Connected to the database!")

	// run db migration (create tables if not exist)
	err = db.RunMigrations(ctx)
	if err != nil {
		log.Fatalf("[ERROR] DATABASE: running migration failed!: %v", err)
	}

	// apply runtime overrides from the database (if present) to the config
	cfg.LoadRuntimeOverrides(ctx, db)

	// setup OIDC for authentication
	provMap, err := auth.SetupProviders(ctx, cfg)
	if err != nil {
		log.Fatalf("[ERROR] AUTH: Could not setup OIDC providers! error: %v", err)
	}

	// initialize session manager
	// secure cookie must be true when app is served over HTTPS
	secureCookie := strings.HasPrefix(cfg.ServerURL, "https://")
	sessionManager := session.NewManager(secureCookie)

	// app context
	appCtx, cancelApp := context.WithCancel(ctx)
	defer cancelApp()

	// initialize notification dispatcher
	disp := dispatcher.New(
		db,
		dispatcher.Config{
			Interval:            cfg.NotificationDispatchInterval,
			MaxRetries:          cfg.NotificationMaxRetries,
			WorkerCount:         cfg.NotificationWorkerCount,
			DisableOnMaxRetries: cfg.NotificationDisableOnMaxRetries,
		},
		dispatcher.EmailConfig{
			Provider:  cfg.EmailProvider,
			ResendKey: cfg.ResendAPIKey,
			FromAddr:  cfg.EmailFromAddr,
			SMTPHost:  cfg.SMTPHost,
			SMTPPort:  cfg.SMTPPort,
			SMTPUser:  cfg.SMTPUser,
			SMTPPass:  cfg.SMTPPass,
			SMTPFrom:  cfg.SMTPFrom,
		},
	)
	disp.Start(appCtx)

	// initialize package version checker
	chk := checker.New(db, checker.Config{
		Interval:     cfg.PackageCheckInterval,
		WorkerCount:  cfg.PackageCheckWorkerCount,
		SkipInterval: cfg.PackageCheckSkipInterval,
	})
	chk.Start(appCtx)

	// initialize notification cleaner
	clnr := cleaner.New(db, cleaner.Config{
		RetentionDays: cfg.NotificationRetentionDays,
	})
	clnr.Start(appCtx)

	// start branch fetcher goroutine that refreshes branch list every 24h
	nix.StartBranchFetcher(appCtx)

	// start cleanup goroutine for stale operation results (entries left behind when user closes browser mid-check)
	packages.StartOperationResultCleanup(appCtx)

	// new request multiplexer
	mux := http.NewServeMux()

	// register routes
	web.RegisterRoutes(mux, cfg, db, provMap, sessionManager, disp, chk, clnr)

	// chain middleware
	chain := middleware.Chain(
		middleware.SecurityHeaders(cfg),
		middleware.RequestLogger,
		sessionManager.LoadAndSave,
	)

	// wrap middleware chain with nosurf CSRF protection
	// validates X-CSRF-Token header (for HTMX requests) and
	// csrf_token form field (for plain HTML forms) on all non-exempt state-changing requests.
	csrfHandler := nosurf.New(chain(mux))
	csrfHandler.ExemptPath("/auth/callback") // exempt: OIDC redirect from provider as there is no token available
	// configure CSRF cookie
	csrfHandler.SetBaseCookie(http.Cookie{
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(cfg.ServerURL, "https://"),
		Path:     "/",
	})
	// return 403 Forbidden with message on CSRF validation failure instead of nosurf default response
	csrfHandler.SetFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "CSRF token invalid or missing", http.StatusForbidden)
	}))

	// server
	server := &http.Server{
		Handler:           csrfHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// graceful shutdown (https://dev.to/yanev/a-deep-dive-into-graceful-shutdown-in-go-484a)

	// channel to listen for errors from the server
	serverErrors := make(chan error, 1)

	// start the server (goroutine) in correct TLS mode
	go func() {
		log.Printf("[INFO] Server is listening on %s port:%s\n", cfg.ServerURL, cfg.ServerPort)
		switch cfg.TLSMode {
		case "on":
			server.Addr = ":" + cfg.ServerPort
			serverErrors <- server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		default: // "off"
			server.Addr = ":" + cfg.ServerPort
			serverErrors <- server.ListenAndServe()
		}
	}()

	// channel to listen for interrupt/terminate signals
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// block until we receive a signal or server error
	select {
	case err := <-serverErrors:
		log.Fatalf("[ERROR] Server failed to start: %v", err)

	case sig := <-shutdown:
		log.Printf("[INFO] Shutdown signal received: %v", sig)

		// stop dispatcher and checker
		cancelApp()

		// give server (in goroutine) time to finish (it could still be processing some requests)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// ask server to shutdown gracefully
		log.Println("[INFO] Shutting down server...")
		if err := server.Shutdown(ctx); err != nil {
			// force close if graceful shutdown fails
			log.Printf("[ERROR] Graceful shutdown failed: %v", err)
			if err := server.Close(); err != nil {
				log.Fatalf("[ERROR] Could not stop server: %v", err)
			}
		}

		log.Println("[INFO] Server stopped gracefully")
	}
}
