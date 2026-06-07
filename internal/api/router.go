package api

import (
	"net/http"
)

// NewRouter creates the HTTP mux with all keypooler routes.
func NewRouter(srv *Server) http.Handler {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("/health", srv.HealthCheck)

	// Key acquisition (used by pulse workers — admin-token protected)
	admin := AdminAuth(srv.Cfg.AdminToken, srv.Logger)
	mux.Handle("/key", admin(http.HandlerFunc(srv.GetKey)))

	// Admin
	mux.Handle("/admin/tiers", admin(http.HandlerFunc(srv.routeTiers)))
	mux.Handle("/admin/keys", admin(http.HandlerFunc(srv.routeKeys)))
	mux.Handle("/admin/keys/", admin(http.HandlerFunc(srv.DeleteKey)))
	mux.Handle("/admin/health", admin(http.HandlerFunc(srv.Health)))

	return RequestLogger(srv.Logger)(mux)
}

func (s *Server) routeTiers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.ListTiers(w, r)
	case http.MethodPost:
		s.CreateTier(w, r)
	case http.MethodPatch:
		s.UpdateTierFeatures(w, r)
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
