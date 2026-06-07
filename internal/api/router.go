package api

import (
	"net/http"
	"strings"
)

// NewRouter creates the HTTP mux with all keypooler routes.
func NewRouter(srv *Server) http.Handler {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("/health", srv.HealthCheck)

	// Key acquisition: admin-OR-consumer auth handled inside GetKey
	// (resolveKeyCaller), NOT AdminAuth, so consumer tokens are accepted.
	mux.Handle("/key", http.HandlerFunc(srv.GetKey))

	// Admin (admin-token only)
	admin := AdminAuth(srv.Cfg.AdminToken, srv.Logger)
	mux.Handle("/admin/tiers", admin(http.HandlerFunc(srv.routeTiers)))
	mux.Handle("/admin/keys", admin(http.HandlerFunc(srv.routeKeys)))
	mux.Handle("/admin/keys/", admin(http.HandlerFunc(srv.DeleteKey)))
	mux.Handle("/admin/health", admin(http.HandlerFunc(srv.Health)))
	mux.Handle("/admin/consumers", admin(http.HandlerFunc(srv.routeConsumers)))
	mux.Handle("/admin/consumers/", admin(http.HandlerFunc(srv.routeConsumerByID)))
	mux.Handle("/admin/usage", admin(http.HandlerFunc(srv.ListUsageEvents)))

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

// routeConsumers handles the collection endpoint /admin/consumers.
func (s *Server) routeConsumers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.ListConsumers(w, r)
	case http.MethodPost:
		s.CreateConsumer(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// routeConsumerByID dispatches /admin/consumers/{id} and
// /admin/consumers/{id}/scopes. ServeMux strips nothing here, so the path tail
// after the prefix decides the action.
func (s *Server) routeConsumerByID(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/admin/consumers/")
	if strings.HasSuffix(tail, "/scopes") {
		s.AddConsumerScope(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		s.DeleteConsumer(w, r)
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
