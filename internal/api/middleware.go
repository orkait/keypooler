package api

import (
	"context"
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
			presented, ok := bearerToken(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing or malformed authorization header")
				return
			}
			if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
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
	scheme, token, ok := strings.Cut(r.Header.Get(authorizationHeader), " ")
	if !ok || !strings.EqualFold(scheme, bearerScheme) {
		return "", false
	}
	return token, true
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
		return keyCaller{isAdmin: true, consumerID: adminConsumerID}, true
	}

	caller, found, err := s.consumerCaller(r.Context(), hashToken(token))
	if err != nil {
		s.Logger.Error().Err(err).Msg("consumer lookup failed")
		writeError(w, http.StatusInternalServerError, "auth lookup failed")
		return keyCaller{}, false
	}
	if !found {
		s.Logger.Warn().Str("remote_addr", r.RemoteAddr).Msg("unauthorized /key access attempt")
		writeError(w, http.StatusUnauthorized, "invalid token")
		return keyCaller{}, false
	}
	return caller, true
}

// consumerCaller resolves a consumer token's hash to its caller: from memory when
// seen recently, else by an indexed lookup confirmed in constant time (no timing
// oracle on the index) and the consumer's scopes.
func (s *Server) consumerCaller(ctx context.Context, tokenHash string) (keyCaller, bool, error) {
	if caller, ok := s.Auth.get(tokenHash); ok {
		return caller, true, nil
	}
	consumer, err := s.DB.GetConsumerByTokenHash(ctx, tokenHash)
	if err != nil {
		return keyCaller{}, false, err
	}
	if consumer == nil || subtle.ConstantTimeCompare([]byte(consumer.TokenHash), []byte(tokenHash)) != 1 {
		return keyCaller{}, false, nil
	}
	scopeIDs, err := s.DB.GetConsumerScopes(ctx, consumer.ID)
	if err != nil {
		return keyCaller{}, false, err
	}
	allowed := make(map[string]bool, len(scopeIDs))
	for _, id := range scopeIDs {
		allowed[id] = true
	}
	caller := keyCaller{consumerID: consumer.ID, allowedTierIDs: allowed}
	s.Auth.put(tokenHash, caller)
	return caller, true, nil
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
