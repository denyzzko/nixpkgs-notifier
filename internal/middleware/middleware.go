package middleware

import (
	"log"
	"net/http"
)

type Middleware func(http.Handler) http.HandlerFunc

func Chain(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.HandlerFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next.ServeHTTP
	}
}

// func Recoverer

func RequestLogger(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("got request: %s %s", r.Method, r.URL.Path)

		next.ServeHTTP(w, r)
	}
}

func RequestAuth(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token != "Bearer token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			log.Printf("unauthorized request from xxx")
			return
		}

		next.ServeHTTP(w, r)
	}
}
