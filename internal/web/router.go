package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
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

func RegisterRoutes(mux *http.ServeMux, db *database.Store, provMap *auth.ProviderMap) {
	mux.HandleFunc("GET /package", getAllPackages(db))
	mux.HandleFunc("GET /package/verify/{pckg}", verifyTracking(db))
	mux.HandleFunc("POST /package/track/{pckg}", createTracking(db))
	//mux.HandleFunc("GET /login", createTracking(db))
	//mux.HandleFunc("GET /auth/callback", createTracking(db))

	//mux.HandleFunc("GET /package/verify/all", verifyAllUsersPackages(db))
	//mux.HandleFunc("POST /user", createUser(db))
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
			return
		}
	}
}

func verifyTracking(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pckgName := r.PathValue("pckg")
		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get package id from name
		var pckgID int64
		packageRow, err := db.QueryPackage(ctx, pckgName)
		if err != nil {
			if err == database.ErrNotFound {
				http.Error(w, "failed to find package you are looking for", http.StatusNotFound)
				return
			} else {
				http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			pckgID = packageRow.ID
		}

		// fetch users tracking row
		trackingRow, err := db.QueryTracking(ctx, 1, pckgID)
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

		// compare if version is up to date with retrieved verison from nix
		upToDate := usersVersion == currentVersion

		// return reponse in json
		verVerf := VersionVerification{Name: pckgName, StoredVrsn: usersVersion, CurrVrsn: currentVersion, UpToDate: upToDate}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(verVerf); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
			return
		}
	}
}

func createTracking(db *database.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		pckgName := r.PathValue("pckg")

		// create context
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		// get current version from nix
		currentVersion, err := nix.GetNixPackageVersionByName(pckgName)
		if err != nil {
			http.Error(w, "failed to get package version from Nix: "+err.Error(), http.StatusBadGateway)
			return
		}

		// get package id from name
		var pckgID int64
		packageRow, err := db.QueryPackage(ctx, pckgName)
		if err != nil {
			if err == database.ErrNotFound {
				// package was not found by name so it should be created
				// since nix eval passed before it actually exists in Nixpkgs -> no need to check
				pckgID, err = db.StorePackage(ctx, pckgName, currentVersion)
				if err != nil {
					http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
					return
				}
			} else {
				http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			pckgID = packageRow.ID
		}

		// store tracking of new package for user (if already exists it will be just updated)
		err = db.StoreTracking(ctx, 1, pckgID, currentVersion)
		if err != nil {
			http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// return reponse
		w.WriteHeader(http.StatusCreated)
	}
}
