package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

// bearerToken extracts the token from a "Bearer <token>" Authorization header.
// Returns ("", false) when the header is missing or malformed.
func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", false
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	return parts[1], true
}

// hashToken returns hex(sha256(token)). Used to store and look up consumer tokens
// without ever persisting the plaintext.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// keyCaller is the authenticated identity behind a /key request.
//   - admin: isAdmin=true, allowedTierIDs=nil (no scope filter), consumerID="admin".
//   - consumer: isAdmin=false, allowedTierIDs=its scoped tier IDs, consumerID=its id.
type keyCaller struct {
	isAdmin        bool
	consumerID     string
	allowedTierIDs map[string]bool
}

// resolveKeyCaller authenticates a /key request as either the admin token or an
// active consumer token. On success it returns the caller and true. On any auth
// failure it writes a 401 and returns false. The /key endpoint deliberately does
// NOT use AdminAuth so it can accept consumer tokens; all /admin/* routes keep
// AdminAuth unchanged.
func (s *Server) resolveKeyCaller(w http.ResponseWriter, r *http.Request) (keyCaller, bool) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing or malformed authorization header")
		return keyCaller{}, false
	}

	// Admin token -> superuser. Constant-time compare against the configured token.
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.Cfg.AdminToken)) == 1 {
		return keyCaller{isAdmin: true, consumerID: "admin", allowedTierIDs: nil}, true
	}

	// Otherwise treat it as a consumer token: indexed lookup by sha256 hash, then
	// a constant-time confirm of the hash to avoid a timing oracle on the index.
	tokenHash := hashToken(token)
	ctx := r.Context()
	consumer, err := s.DB.GetConsumerByTokenHash(ctx, tokenHash)
	if err != nil {
		s.Logger.Error().Err(err).Msg("consumer lookup failed")
		writeError(w, http.StatusInternalServerError, "auth lookup failed")
		return keyCaller{}, false
	}
	if consumer == nil || subtle.ConstantTimeCompare([]byte(consumer.TokenHash), []byte(tokenHash)) != 1 {
		s.Logger.Warn().Str("remote_addr", r.RemoteAddr).Msg("unauthorized /key access attempt")
		writeError(w, http.StatusUnauthorized, "invalid token")
		return keyCaller{}, false
	}

	scopeIDs, err := s.DB.GetConsumerScopes(ctx, consumer.ID)
	if err != nil {
		s.Logger.Error().Err(err).Str("consumer_id", consumer.ID).Msg("consumer scope lookup failed")
		writeError(w, http.StatusInternalServerError, "auth lookup failed")
		return keyCaller{}, false
	}
	allowed := make(map[string]bool, len(scopeIDs))
	for _, id := range scopeIDs {
		allowed[id] = true
	}
	return keyCaller{isAdmin: false, consumerID: consumer.ID, allowedTierIDs: allowed}, true
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
