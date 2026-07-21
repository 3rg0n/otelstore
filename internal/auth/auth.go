package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// bearerPrefix is the required prefix of the Authorization header value.
const bearerPrefix = "Bearer "

// TokenValid reports whether the Authorization header value carries the
// expected bearer token. It requires the exact "Bearer " prefix (not merely a
// long-enough header) and compares the token in constant time. Shared by the
// HTTP middleware and the gRPC interceptors so all transports enforce auth
// identically.
func TokenValid(authHeader, expected string) bool {
	rest, ok := strings.CutPrefix(authHeader, bearerPrefix)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rest), []byte(expected)) == 1
}

// unauthenticatedPaths bypass bearer auth so external health-checkers (Traefik,
// k8s, container runtimes) can probe without a token. These expose no telemetry.
var unauthenticatedPaths = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
}

// Middleware wraps an http.Handler with optional bearer token authentication.
// If token is empty, requests pass through unchanged.
// If token is set, requires header "Authorization: Bearer <token>" — except for
// the health/readiness probe paths, which are always open.
func Middleware(authToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if authToken == "" || unauthenticatedPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			if !TokenValid(r.Header.Get("Authorization"), authToken) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
