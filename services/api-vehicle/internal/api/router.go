package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

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
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := NewHandler(cfg)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(auth.AuthMiddleware(cfg))
		
		r.Post("/vehicles", h.CreateVehicle)
		r.Delete("/vehicles/{id}", h.SoftDeleteVehicle)
		r.Post("/vehicles/{id}/restore", h.RestoreVehicle)
		
		r.Post("/geofences", h.CreateGeofence)
		r.Post("/routes", h.CreateRoute)
	})

	return r
}
