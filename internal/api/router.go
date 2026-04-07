package api

import (
	"net/http"
)

// NewRouter creates the HTTP mux with all routes.
func NewRouter(srv *Server) http.Handler {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("/health", srv.HealthCheck)
	mux.HandleFunc("/api/execute", srv.Execute)
	mux.HandleFunc("/api/executions/", srv.GetExecution)

	// Admin (auth required)
	admin := AdminAuth(srv.Cfg.AdminToken, srv.Logger)
	mux.Handle("/admin/tiers", admin(http.HandlerFunc(srv.routeTiers)))
	mux.Handle("/admin/keys", admin(http.HandlerFunc(srv.routeKeys)))
	mux.Handle("/admin/keys/", admin(http.HandlerFunc(srv.DeleteKey)))
	mux.Handle("/admin/integrations", admin(http.HandlerFunc(srv.routeIntegrations)))
	mux.Handle("/admin/integrations/versions/", admin(http.HandlerFunc(srv.ActivateIntegrationVersion)))
	mux.Handle("/admin/health", admin(http.HandlerFunc(srv.Health)))
	mux.Handle("/admin/dead-letter", admin(http.HandlerFunc(srv.GetDeadLetters)))
	mux.Handle("/admin/dead-letter/", admin(http.HandlerFunc(srv.RetryDeadLetter)))

	return RequestLogger(srv.Logger)(mux)
}

func (s *Server) routeTiers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.ListTiers(w, r)
	case http.MethodPost:
		s.CreateTier(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.ListKeys(w, r)
	case http.MethodPost:
		s.AddKey(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeIntegrations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.ListIntegrations(w, r)
	case http.MethodPost:
		s.CreateIntegrationVersion(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
