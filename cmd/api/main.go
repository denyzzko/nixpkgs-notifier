package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/middleware"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
)

// just mock data
type Package struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

var packages = []Package{
	{ID: "1", Name: "python3", Version: "1.1.2"},
	{ID: "2", Name: "firefox", Version: "1.0.0"},
	{ID: "3", Name: "openconnect", Version: "2.1.3"},
}

func getAllPackages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// comment
	if err := json.NewEncoder(w).Encode(packages); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func getPackageVersionByName(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	nix.GetNixPackageByName(name)
}

func main() {
	// check nix availability
	ok := nix.CheckNixAvailability()
	if !ok {
		log.Fatal("error nix is not available")
	}

	// new request multiplexer
	mux := http.NewServeMux()

	// register routes
	mux.HandleFunc("GET /package", getAllPackages)
	mux.HandleFunc("GET /package/version/{name}", getPackageVersionByName)
	//mux.HandleFunc("POST /package", createPackage)
	//mux.HandleFunc("DELETE /package", deletePackage)

	// chain all middlewares
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
