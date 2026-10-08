package api

import "net/http"

// pathID is the {id} wildcard in the routes below, read with r.PathValue.
const pathID = "id"

// NewRouter creates the HTTP mux with all keypooler routes. Each pattern names
// its method, so the mux answers any other method with 405 before a handler runs.
func NewRouter(srv *Server) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", srv.HealthCheck)

	// Admin-OR-consumer auth is resolved inside these two (resolveKeyCaller), not
	// by AdminAuth, so consumer tokens are accepted.
	mux.HandleFunc("GET /key", srv.GetKey)
	mux.HandleFunc("POST /key/{id}/exhausted", srv.ExhaustKey)

	// Admin-token only. A write clears the consumer auth cache.
	authorized := AdminAuth(srv.Cfg.AdminToken, srv.Logger)
	admin := func(h http.HandlerFunc) http.Handler { return authorized(srv.Auth.clearAfterWrite(h)) }
	mux.Handle("GET /admin/tiers", admin(srv.ListTiers))
	mux.Handle("POST /admin/tiers", admin(srv.CreateTier))
	mux.Handle("PATCH /admin/tiers", admin(srv.UpdateTierFeatures))
	mux.Handle("GET /admin/keys", admin(srv.ListKeys))
	mux.Handle("POST /admin/keys", admin(srv.AddKey))
	mux.Handle("DELETE /admin/keys/{id}", admin(srv.DeleteKey))
	mux.Handle("GET /admin/health", admin(srv.Health))
	mux.Handle("GET /admin/consumers", admin(srv.ListConsumers))
	mux.Handle("POST /admin/consumers", admin(srv.CreateConsumer))
	mux.Handle("DELETE /admin/consumers/{id}", admin(srv.DeleteConsumer))
	mux.Handle("POST /admin/consumers/{id}/scopes", admin(srv.AddConsumerScope))
	mux.Handle("GET /admin/usage", admin(srv.ListUsageEvents))

	if !srv.Cfg.LogRequests {
		return mux
	}
	return RequestLogger(srv.Logger)(mux)
}
