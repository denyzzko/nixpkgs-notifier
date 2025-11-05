package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
	"github.com/denyzzko/nixpkgs-notifier/internal/session"
	"golang.org/x/oauth2"
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

func RegisterRoutes(ctx context.Context, mux *http.ServeMux, db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager) {
	mux.HandleFunc("GET /package", getAllPackages(db))
	mux.HandleFunc("GET /package/verify/{pckg}", verifyTracking(db))
	mux.HandleFunc("POST /package/track/{pckg}", createTracking(db))
	mux.HandleFunc("GET /auth/login", login(provMap, sessionManager))
	mux.HandleFunc("GET /auth/callback", callback(ctx, db, provMap, sessionManager))
	mux.HandleFunc("GET /me", me(sessionManager))

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

func login(provMap *auth.ProviderMap, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// block of code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 66-79)
		// get provider from query
		name := r.URL.Query().Get("provider")
		prov, ok := provMap.Providers[name]
		if !ok {
			http.Error(w, "unknown provider", http.StatusBadRequest)
			return
		}

		// generate secrets (random state, nonce, code verifier and challenge)
		secrets, err := auth.CreateOIDCSecrets()
		if err != nil {
			http.Error(w, "internal error while creating secrets", http.StatusInternalServerError)
			return
		}

		// store necessary oidc data in session
		sessionManager.SaveOIDCSecrets(r.Context(), secrets.State, session.OIDCAuthData{
			Nonce:        secrets.Nonce,
			CodeVerifier: secrets.CodeVerifier,
			Provider:     name,
		})

		// build redirect url
		authURL := prov.Config.AuthCodeURL(
			secrets.State,
			oidc.Nonce(secrets.Nonce),
			oauth2.SetAuthURLParam("code_challenge", secrets.CodeChallenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)

		// redirect user to the provider's login page with all necessary data
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func callback(ctx context.Context, db *database.Store, provMap *auth.ProviderMap, sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// block of code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 82-136)

		// get state from the query
		state := r.URL.Query().Get("state")
		if state == "" {
			http.Error(w, "missing authorization state", http.StatusBadRequest)
			return
		}

		// pop oidc data from session (verifies also match of state)
		data, ok := sessionManager.PopOIDCSecrets(r.Context(), state)
		if !ok {
			http.Error(w, "unknown/expired state", http.StatusBadRequest)
			return
		}

		// get provider by name
		prov, ok := provMap.Providers[data.Provider]
		if !ok {
			http.Error(w, "unknown provider", http.StatusBadRequest)
			return
		}

		// get code from the query
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			return
		}

		// send code and code verifier to providers token endpoint
		// provider sends back token that is used to get user information
		oauth2Token, err := prov.Config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", data.CodeVerifier))
		if err != nil {
			http.Error(w, "failed to exchange token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// get the ID token (JWT) from the token response
		rawIDToken, ok := oauth2Token.Extra("id_token").(string)
		if !ok {
			http.Error(w, "no id_token field in oauth2 token", http.StatusInternalServerError)
			return
		}

		// verify ID token
		idToken, err := prov.Verifier.Verify(ctx, rawIDToken)
		if err != nil {
			http.Error(w, "failed to verify ID Token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// get nonce that was stored in a session and verify it macthes the one sent by provider in idtoken
		if idToken.Nonce != data.Nonce {
			http.Error(w, "nonce did not match", http.StatusBadRequest)
			return
		}

		// pull user info from the token
		var claims struct {
			Sub               string `json:"sub"`
			Email             string `json:"email"`
			EmailVerified     bool   `json:"email_verified"`
			PreferredUsername string `json:"preferred_username"`
		}
		if err := idToken.Claims(&claims); err != nil {
			http.Error(w, "claims parse failed: "+err.Error(), http.StatusBadRequest)
			return
		}

		// renew session token
		err = sessionManager.RenewToken(r.Context())
		if err != nil {
			http.Error(w, "error renewing token "+err.Error(), http.StatusInternalServerError)
		}

		// map external identity -> local user
		issuer := prov.Issuer
		sub := claims.Sub

		// get user by (issuer, sub)
		var userID int64
		accountRow, err := db.QueryAccountByIssuerSub(r.Context(), issuer, sub)
		if err != nil {
			if err == database.ErrNotFound {
				// account was not found -> create user and account for this subject
				userID, err = db.CreateUserWithAccount(r.Context(), database.UserInfo{
					Email:         &claims.Email,
					EmailVerified: claims.EmailVerified,
					Username:      &claims.PreferredUsername,
					Role:          "user",
					Provider:      prov.Name,
					Issuer:        issuer,
					Subject:       sub,
				})
				if err != nil {
					http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
					return
				}
			} else {
				http.Error(w, "some database error occured: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			userID = accountRow.UserID
		}

		// store user id in session
		sessionManager.Put(r.Context(), "userID", userID)

		// redirect user to the home page
		// currently random /me that proves user is logged in for testing
		http.Redirect(w, r, "/me", http.StatusFound)
	}
}

func me(sessionManager *session.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := sessionManager.GetUserID(r.Context())
		if uid == 0 {
			http.Error(w, "not logged in", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "Logged in. user with id:%d\n", uid)
	}
}
