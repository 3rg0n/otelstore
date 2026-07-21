package auth

import (
	"crypto/subtle"
	"log"
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

// sanitizeForLog strips control characters (notably CR/LF) from a value before
// it is written to a log line, preventing log-injection/forging (CWE-117) via
// attacker-influenced fields like the request path or remote address.
func sanitizeForLog(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, s)
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
				// Audit the rejection with source + path + reason. Never log the
				// token or the Authorization header value itself. Fields are
				// sanitized to prevent log injection (CWE-117) via crafted paths.
				reason := "invalid token"
				if r.Header.Get("Authorization") == "" {
					reason = "missing authorization header"
				}
				// #nosec G706 -- fields pass through sanitizeForLog (strips CR/LF
				// and control chars), neutralizing log injection.
				log.Printf("auth: rejected %s %s from %s (%s)",
					sanitizeForLog(r.Method), sanitizeForLog(r.URL.Path), sanitizeForLog(r.RemoteAddr), reason)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
