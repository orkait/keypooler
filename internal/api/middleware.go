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

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get(headerAuthorization), " ")
	if !ok || !strings.EqualFold(scheme, bearerScheme) {
		return "", false
	}
	return token, true
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type keyCaller struct {
	consumerID     string
	allowedTierIDs map[string]bool
}

func (s *Server) resolveKeyCaller(w http.ResponseWriter, r *http.Request) (keyCaller, bool) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing or malformed authorization header")
		return keyCaller{}, false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.AdminToken)) == 1 {
		return keyCaller{consumerID: adminConsumerID}, true
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
