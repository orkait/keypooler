package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
)

// AdminAuth middleware checks the Authorization header for a valid admin token.
func AdminAuth(token string, logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				writeError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			parts := strings.SplitN(auth, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				writeError(w, http.StatusUnauthorized, "invalid authorization format")
				return
			}

			if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
				logger.Warn().Str("remote_addr", r.RemoteAddr).Msg("unauthorized admin access attempt")
				writeError(w, http.StatusUnauthorized, "invalid admin token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger middleware logs each incoming request.
func RequestLogger(logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Str("remote_addr", r.RemoteAddr).
				Msg("request received")
			next.ServeHTTP(w, r)
		})
	}
}
