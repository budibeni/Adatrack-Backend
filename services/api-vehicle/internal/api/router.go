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
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	h := NewHandler(cfg)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(auth.AuthMiddleware(cfg))

		// Vehicles
		r.Get("/vehicles", h.ListVehicles)
		r.Get("/vehicles/{id}", h.GetVehicle)
		r.Post("/vehicles", h.CreateVehicle)
		r.Put("/vehicles/{id}", h.UpdateVehicle)
		r.Delete("/vehicles/{id}", h.SoftDeleteVehicle)
		r.Post("/vehicles/{id}/restore", h.RestoreVehicle)

		// Geofences
		r.Get("/geofences", h.ListGeofences)
		r.Get("/geofences/{id}", h.GetGeofence)
		r.Post("/geofences", h.CreateGeofence)
		r.Put("/geofences/{id}", h.UpdateGeofence)
		r.Delete("/geofences/{id}", h.SoftDeleteGeofence)
		r.Post("/geofences/{id}/restore", h.RestoreGeofence)

		// Routes
		r.Get("/routes", h.ListRoutes)
		r.Get("/routes/{id}", h.GetRoute)
		r.Post("/routes", h.CreateRoute)
		r.Delete("/routes/{id}", h.SoftDeleteRoute)
		r.Post("/routes/{id}/restore", h.RestoreRoute)
		r.Post("/routes/{id}/assignments", h.AssignRoute)
		r.Put("/routes/{id}/status", h.UpdateRouteStatus)

		// Speed Configs
		r.Get("/speed-configs", h.ListSpeedConfigs)
		r.Post("/speed-configs", h.CreateSpeedConfig)
		r.Delete("/speed-configs/{id}", h.SoftDeleteSpeedConfig)
		r.Post("/speed-configs/{id}/restore", h.RestoreSpeedConfig)

		// Notification Preferences
		r.Get("/notification-preferences", h.GetNotificationPreferences)
		r.Post("/notification-preferences", h.SetNotificationPreference)

		// Alerts Lifecycle
		r.Get("/alerts", h.ListAlerts)
		r.Post("/alerts/{id}/acknowledge", h.AcknowledgeAlert)
		r.Post("/alerts/{id}/resolve", h.ResolveAlert)

		// Fuel
		r.Post("/fuel-configs", h.CreateFuelConfig)
		r.Put("/fuel-configs/{id}", h.UpdateFuelConfig)
		r.Get("/vehicles/{id}/fuel/history", h.GetFuelHistory)

		// Media Events (Scope A)
		r.Post("/media/events", h.CreateMediaEvent)
		r.Post("/media/events/{id}/complete", h.CompleteMediaEvent)
		r.Get("/media/{id}/url", h.GetMediaURL)
		r.Delete("/media/{id}", h.DeleteMediaEvent)
		r.Post("/media/{id}/restore", h.RestoreMediaEvent)
	})

	return r
}
