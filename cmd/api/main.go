package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/middleware"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
)

type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// just mock data for specific user
var packages = []Package{
	{Name: "python3", Version: "1.1.2"},
	{Name: "firefox", Version: "139.0.0"},
	{Name: "openconnect", Version: "2.1.3"},
}

func getAllPackages(w http.ResponseWriter, r *http.Request) {
	// return packages in json
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(packages); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func getPackageVersionByName(w http.ResponseWriter, r *http.Request) {
	// get version from nix
	name := r.PathValue("name")
	version, err := nix.GetNixPackageVersionByName(name)

	// handle error
	if err != nil {
		http.Error(w, "failed to get package version:\n"+err.Error(), http.StatusBadGateway)
		return
	}

	// return package version in json
	response := Package{Name: name, Version: version}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func verifyPackageByName(w http.ResponseWriter, r *http.Request) {
	// get version from nix
	name := r.PathValue("name")
	version, err := nix.GetNixPackageVersionByName(name)

	// handle error
	if err != nil {
		http.Error(w, "failed to get package version: "+err.Error(), http.StatusBadGateway)
	}

	fmt.Println(version)

	// verify with version stored in db

	// return response to user (version is ok or no)
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
	mux.HandleFunc("GET /package/verify/{name}", verifyPackageByName)
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
