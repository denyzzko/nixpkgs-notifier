package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
)

type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type VersionVerification struct {
	Name       string `json:"name"`
	StoredVrsn string `json:"storedVersion"`
	CurrVrsn   string `json:"currentVersion"`
	UpToDate   bool   `json:"upToDate"`
}

func RegisterRoutes(mux *http.ServeMux, db *database.Store) {
	mux.HandleFunc("GET /package", getAllPackages(db))
	mux.HandleFunc("GET /package/verify/{pckg}", verifyUsersPackageByName(db))
	//mux.HandleFunc("POST /package", createPackage)
	//mux.HandleFunc("DELETE /package", deletePackage)
}

func getAllPackages(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		// get packages from db
		packageRows, err := db.QueryAllPackages(ctx)
		if err != nil {
			http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// format to type Package
		var packages []Package
		for _, pckg := range packageRows {
			packages = append(packages, Package{Name: pckg.Name, Version: pckg.Version})
		}

		// return packages in json
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(packages); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}

func verifyUsersPackageByName(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pckgName := r.PathValue("pckg")
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// fetch users tracking row
		trackingRow, err := db.QueryUsersPackageByName(ctx, 1, pckgName)
		if err != nil {
			if err == database.ErrNotFound {
				http.Error(w, "failed to find your package, you are not tracking it", http.StatusNotFound)
			} else {
				http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
		usersVersion := trackingRow.UsersVersion

		// get current version from nix
		currentVersion, err := nix.GetNixPackageVersionByName(pckgName)
		if err != nil {
			http.Error(w, "failed to get package version from Nix: "+err.Error(), http.StatusBadGateway)
			return
		}

		// compare if version is up tp date with retrieved verison from nix
		upToDate := usersVersion == currentVersion

		// return reponse in json
		verVerf := VersionVerification{Name: pckgName, StoredVrsn: usersVersion, CurrVrsn: currentVersion, UpToDate: upToDate}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(verVerf); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}
