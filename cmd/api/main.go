package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/middleware"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
	"github.com/denyzzko/nixpkgs-notifier/internal/web"
)

func main() {
	// check nix availability
	ok := nix.CheckNixAvailability()
	if !ok {
		log.Fatal("Error nix is not available")
	}

	// open connection to db
	db, err := database.Open(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	log.Println("Connected to the database!")

	// new request multiplexer
	mux := http.NewServeMux()

	// register routes
	web.RegisterRoutes(mux, db)

	// chain middleware
	chain := middleware.Chain(
		middleware.RequestLogger,
		middleware.RequestAuth,
	)

	// server
	server := &http.Server{
		Addr:              ":8080",
		Handler:           chain(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// run server
	log.Println("listening on http://localhost:8080")
	log.Fatal(server.ListenAndServe())
}
