package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/denyzzko/nixpkgs-notifier/internal/auth"
	"github.com/denyzzko/nixpkgs-notifier/internal/database"
	"github.com/denyzzko/nixpkgs-notifier/internal/nix"
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

func RegisterRoutes(ctx context.Context, mux *http.ServeMux, db *database.Store, provMap *auth.ProviderMap) {
	mux.HandleFunc("GET /package", getAllPackages(db))
	mux.HandleFunc("GET /package/verify/{pckg}", verifyTracking(db))
	mux.HandleFunc("POST /package/track/{pckg}", createTracking(db))
	mux.HandleFunc("GET /auth/login", login(provMap))
	mux.HandleFunc("GET /auth/callback", callback(ctx, provMap))
	mux.HandleFunc("GET /me", me())

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

// Random base64url string, used for state/nonce/verifier generation
func randString(nByte int) (string, error) {
	b := make([]byte, nByte)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Set a short-lived, httpOnly cookie (used for OIDC handshake)
func setCallbackCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",  // send on all paths
		MaxAge:   600,  // 10 minutes
		HttpOnly: true, // not readable by JS
		Secure:   r.TLS != nil,
	}
	http.SetCookie(w, c)
}

// Delete cookies (that were used for OIDC handshake)
func clearCallbackCookies(w http.ResponseWriter) {
	names := []string{"state", "nonce", "code_verifier", "oidc_provider"}
	for _, n := range names {
		http.SetCookie(w, &http.Cookie{
			Name:    n,
			Value:   "",
			Path:    "/",
			Expires: time.Unix(0, 0),
			MaxAge:  -1,
		})
	}
}

// PKCE S256 transform: base64url(SHA256(code_verifier))
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	return challenge
}

func login(provMap *auth.ProviderMap) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// block of code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 66-79)
		// get provider from query
		name := r.URL.Query().Get("provider")
		prov, ok := provMap.Providers[name]
		if !ok {
			http.Error(w, "unknown provider", http.StatusBadRequest)
			return
		}

		// generate random state, nonce and code verifier
		state, err := randString(16)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		nonce, err := randString(16)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		verifier, err := randString(32)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// PKCE S256
		challenge := pkceS256(verifier)

		// store them in cookies
		// along with provider name so it can be distinguished later
		// will be changed later to be saved server-side
		setCallbackCookie(w, r, "state", state)
		setCallbackCookie(w, r, "nonce", nonce)
		setCallbackCookie(w, r, "code_verifier", verifier)
		setCallbackCookie(w, r, "oidc_provider", name)

		// build redirect url
		authURL := prov.Config.AuthCodeURL(
			state,
			oidc.Nonce(nonce),
			oauth2.SetAuthURLParam("code_challenge", challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)

		// redirect user to the provider's login page with all necessary data
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func callback(ctx context.Context, provMap *auth.ProviderMap) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// block of code from: https://github.com/coreos/go-oidc/blob/v3/example/idtoken/app.go (line 82-136)
		// get provider name that was stored in cookie
		name, err := r.Cookie("oidc_provider")
		if err != nil {
			http.Error(w, "provider name not found", http.StatusBadRequest)
			return
		}
		prov, ok := provMap.Providers[name.Value]
		if !ok {
			http.Error(w, "unknown provider", http.StatusBadRequest)
			return
		}

		// get state that was stored in cookie and verify it is same as the one in query
		state, err := r.Cookie("state")
		if err != nil {
			http.Error(w, "state not found", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("state") != state.Value {
			http.Error(w, "state did not match", http.StatusBadRequest)
			return
		}

		// get code verifier
		vck, err := r.Cookie("pkce_verifier")
		if err != nil {
			http.Error(w, "verifier not found", http.StatusBadRequest)
			return
		}

		// get code from the query
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			return
		}

		// send code and code verifier (and other data like client_id) to providers token endpoint
		// provider sends back token that is used to get user information
		oauth2Token, err := prov.Config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", vck.Value))
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

		// get nonce that was stored in a cookie and verify it macthes the one sent by provider in idtoken
		nonce, err := r.Cookie("nonce")
		if err != nil {
			http.Error(w, "nonce not found", http.StatusBadRequest)
			return
		}
		if idToken.Nonce != nonce.Value {
			http.Error(w, "nonce did not match", http.StatusBadRequest)
			return
		}

		// pull user info from the token
		var claims struct {
			Sub           string `json:"sub"`
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
		}
		if err := idToken.Claims(&claims); err != nil {
			http.Error(w, "claims parse failed: "+err.Error(), http.StatusBadRequest)
			return
		}

		// temporary
		// TODO: sessions
		http.SetCookie(w, &http.Cookie{
			Name:     "demo_session_user",
			Value:    prov.URL + "|" + claims.Sub,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil,
			// Secure: true in HTTPS prod
		})

		// delete data that were stored in cookies as they are no longer needed
		clearCallbackCookies(w)

		// redirect user to the home page
		// currently random /me that proves user is logged in for testing
		http.Redirect(w, r, "/me", http.StatusFound)
	}
}

func me() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("demo_session_user")
		if err != nil {
			http.Error(w, "not logged in", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "Logged in as %s\n", c.Value)
	}
}
