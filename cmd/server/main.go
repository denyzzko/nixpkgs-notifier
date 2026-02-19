package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/env"
	"github.com/denyzzko/nixpkgs-notifier/internal/middleware"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"github.com/denyzzko/nixpkgs-notifier/internal/web"
)

func main() {
	ctx := context.Background()

	// load configuration
	cfg, err := env.LoadEnvConfig()
	if err != nil {
		log.Fatalf("[ERROR] ENV: Could not read .env file!: %v", err)
	}

	// check nix availability
	err = nix.CheckNixAvailability()
	if err != nil {
		log.Fatalf("[ERROR] NIX: Nix is not available on this system!: %v", err)
	}

	// open connection to db
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	log.Println("[INFO] Connected to the database!")

	// setup OIDC for authentication
	provMap, err := auth.SetupProviders(ctx, cfg)
	if err != nil {
		log.Fatalf("[ERROR] AUTH: Could not setup OIDC providers! error: %v", err)
	}

	// initialize session manager
	sessionManager := session.NewManager()

	// new request multiplexer
	mux := http.NewServeMux()

	// register routes
	web.RegisterRoutes(mux, db, provMap, sessionManager)

	// chain middleware
	chain := middleware.Chain(
		middleware.RequestLogger,
		sessionManager.LoadAndSave,
		//middleware.RequestAuth,
	)

	// server
	server := &http.Server{
		Addr:              ":8080",
		Handler:           chain(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// graceful shutdown (https://dev.to/yanev/a-deep-dive-into-graceful-shutdown-in-go-484a)

	// channel to listen for errors from the server
	serverErrors := make(chan error, 1)

	// start the server (goroutine)
	go func() {
		log.Printf("[INFO] Server is listening on %s\n", cfg.ServerURL)
		serverErrors <- server.ListenAndServe()
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
