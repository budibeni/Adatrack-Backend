package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"backend/internal/config"
	"backend/internal/auth"
	"backend/service-websocket/internal/ws"
)

func SetupRouter(cfg *config.Config, hub *ws.Hub) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"}, // Adjust as needed
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	h := NewHandler(cfg, hub)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", h.Login)

		r.Group(func(r chi.Router) {
			r.Use(auth.AuthMiddleware(cfg))
			
			r.Post("/auth/refresh", h.Refresh)
			r.Post("/auth/logout", h.Logout)
			
			// Platform endpoints
			r.With(auth.EnsurePlatformAdminMiddleware).Post("/companies", h.CreateCompany)
			
			// Tenant endpoints
			r.Get("/vehicles", h.ListVehicles)
			r.Get("/vehicles/{id}", h.GetVehicle)
			r.Get("/vehicles/{id}/history", h.GetVehicleHistory)
			
			// Websocket endpoint
			r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
				ws.ServeWS(hub, w, r, cfg) // Handshake also checks JWT via query/header
			})
		})
	})

	return r
}
