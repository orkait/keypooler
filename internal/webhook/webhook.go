package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// Send posts the payload to the callback URL. Fire-and-forget with single retry.
// Uses its own timeout context so it survives parent cancellation (e.g. shutdown).
func Send(_ context.Context, callbackURL string, payload any, logger zerolog.Logger) {
	go func() {
		body, err := json.Marshal(payload)
		if err != nil {
			logger.Error().Err(err).Str("url", callbackURL).Msg("webhook: failed to marshal payload")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for attempt := 0; attempt < 2; attempt++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
			if err != nil {
				logger.Error().Err(err).Str("url", callbackURL).Msg("webhook: failed to create request")
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			if err != nil {
				logger.Warn().Err(err).
					Str("url", callbackURL).
					Int("attempt", attempt+1).
					Msg("webhook: request failed")
				time.Sleep(2 * time.Second)
				continue
			}
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				logger.Debug().Str("url", callbackURL).Msg("webhook: delivered")
				return
			}

			logger.Warn().
				Str("url", callbackURL).
				Int("status", resp.StatusCode).
				Int("attempt", attempt+1).
				Msg("webhook: non-2xx response")
			time.Sleep(2 * time.Second)
		}
	}()
}
