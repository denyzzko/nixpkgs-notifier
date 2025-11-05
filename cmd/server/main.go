package main

import (
	"context"
	"log"
	"net/http"
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
		log.Fatal("[ERROR] ENV: Could not read .env file!")
	}

	// check nix availability
	ok := nix.CheckNixAvailability()
	if !ok {
		log.Fatal("[ERROR] NIX: Nix is not available!")
	}

	// open connection to db
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	log.Println("Connected to the database!")

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
	web.RegisterRoutes(ctx, mux, db, provMap, sessionManager)

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
	}

	// run server
	log.Printf("Server is listening on %s\n", cfg.ServerURL)
	log.Fatal(server.ListenAndServe())
}
