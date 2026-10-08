package api

import "net/http"

func NewRouter(srv *Server) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", srv.HealthCheck)
	mux.HandleFunc("GET /key", srv.GetKey)
	mux.HandleFunc("POST /key/{id}/exhausted", srv.ExhaustKey)
	mux.HandleFunc("POST /key/{id}/spend", srv.ReportSpend)

	authorized := AdminAuth(srv.AdminToken, srv.Logger)
	admin := func(h http.HandlerFunc) http.Handler { return authorized(srv.Auth.clearAfterWrite(h)) }
	mux.Handle("GET /admin/tiers", admin(srv.ListTiers))
	mux.Handle("POST /admin/tiers", admin(srv.CreateTier))
	mux.Handle("PATCH /admin/tiers", admin(srv.UpdateTierFeatures))
	mux.Handle("GET /admin/keys", admin(srv.ListKeys))
	mux.Handle("POST /admin/keys", admin(srv.AddKey))
	mux.Handle("PATCH /admin/keys/{id}", admin(srv.SetKeyBudget))
	mux.Handle("DELETE /admin/keys/{id}", admin(srv.DeleteKey))
	mux.Handle("GET /admin/health", admin(srv.Health))
	mux.Handle("GET /admin/consumers", admin(srv.ListConsumers))
	mux.Handle("POST /admin/consumers", admin(srv.CreateConsumer))
	mux.Handle("DELETE /admin/consumers/{id}", admin(srv.DeleteConsumer))
	mux.Handle("POST /admin/consumers/{id}/scopes", admin(srv.AddConsumerScope))
	mux.Handle("GET /admin/usage", admin(srv.ListUsageEvents))
	return mux
}
