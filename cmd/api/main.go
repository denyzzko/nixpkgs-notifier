package main

import (
	"encoding/json"
	"log"
	"net/http"
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
	if err := json.NewEncoder(w).Encode(packages); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func main() {
	// new request multiplexer
	mux := http.NewServeMux()

	// register routes
	mux.HandleFunc("GET /package", getAllPackages)
	//mux.HandleFunc("GET /package/{id}", getPackageByID )
	//mux.HandleFunc("POST /package", createPackage)
	//mux.HandleFunc("DELETE /package", deletePackage)

	// run server
	http.ListenAndServe("localhost:8080", mux)
	log.Println("Server listening on http://localhost:8080")
}
