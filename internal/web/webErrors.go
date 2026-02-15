package web

import (
	"log"
	"net/http"

	"github.com/denyzzko/nixpkgs-notifier/internal/appError"
)

func HTTPStatus(err error) int {
	switch appError.ErrorCause(err) {
	case appError.Invalid:
		return http.StatusBadRequest
	case appError.Unauthenticated:
		return http.StatusUnauthorized
	case appError.Forbidden:
		return http.StatusForbidden
	case appError.NotFound:
		return http.StatusNotFound
	case appError.Conflict:
		return http.StatusConflict
	case appError.Upstream:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func writeAppErr(w http.ResponseWriter, op string, err error) {
	log.Printf("[ERROR] request failed: %s: %v", op, err)
	http.Error(w, appError.PublicMessage(err), HTTPStatus(err))
}

func writeGenericErr(w http.ResponseWriter, op string, msg string, err error, status int) {
	log.Printf("[ERROR] request failed: %s: %s: %v", op, msg, err)
	http.Error(w, msg, status)
}
