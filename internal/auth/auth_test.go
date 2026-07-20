package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenValid(t *testing.T) {
	const secret = "s3cret-token"
	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"correct bearer", "Bearer s3cret-token", true},
		{"wrong token", "Bearer wrong", false},
		{"empty header", "", false},
		{"missing prefix", "s3cret-token", false},
		// Regression: a non-Bearer scheme of sufficient length must be rejected.
		// The prior implementation only checked len(header) >= len("Bearer ")
		// and blindly sliced the first 7 bytes, so "Token=" style headers passed.
		{"wrong scheme same length", "Token= s3cret-token", false},
		{"seven-char junk prefix", "XXXXXXXs3cret-token", false},
		{"bearer lowercase", "bearer s3cret-token", false},
		{"prefix only", "Bearer ", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TokenValid(tc.header, secret); got != tc.want {
				t.Errorf("TokenValid(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestMiddlewareNoToken(t *testing.T) {
	// Empty configured token => auth disabled, everything passes.
	h := Middleware("")(okHandler())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("no-auth: got %d, want 200", rr.Code)
	}
}

func TestMiddlewareWithToken(t *testing.T) {
	h := Middleware("s3cret-token")(okHandler())

	cases := []struct {
		name       string
		authHeader string
		wantCode   int
	}{
		{"correct", "Bearer s3cret-token", http.StatusOK},
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"wrong scheme", "Token= s3cret-token", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.wantCode {
				t.Errorf("%s: got %d, want %d", tc.name, rr.Code, tc.wantCode)
			}
		})
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}
