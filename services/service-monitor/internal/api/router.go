package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"backend/internal/auth"
	"backend/internal/config"
)

func SetupRouter(cfg *config.Config) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Business-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	
	r.Handle("/metrics", promhttp.Handler())

	h := NewHandler()

	r.Route("/api/v1/monitor", func(r chi.Router) {
		r.Use(auth.AuthMiddleware(cfg))
		r.Use(auth.EnsurePlatformAdminMiddleware)

		r.Get("/services", h.ListServices)
		r.Post("/services/{id}/start", h.StartService)
		r.Post("/services/{id}/stop", h.StopService)
		r.Get("/services/{id}/logs", h.GetLogs)
	})

	return r
}
