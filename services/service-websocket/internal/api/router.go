package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

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
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Business-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	h := NewHandler(cfg, hub)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", h.Login)
		r.Post("/auth/refresh", h.Refresh)

		r.Group(func(r chi.Router) {
			r.Use(auth.AuthMiddleware(cfg))
			
			r.Post("/auth/logout", h.Logout)
			
			// Platform endpoints
			r.With(auth.EnsurePlatformAdminMiddleware).Post("/companies", h.CreateCompany)
			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/dashboard", h.GetDashboardStats)
			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/global-devices", h.GetGlobalDevices)

			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/sim-cards", h.GetSimCards)

			r.With(auth.EnsurePlatformAdminMiddleware).Post("/admin/sim-cards", h.CreateSimCard)

			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/broadcasts", h.GetBroadcasts)

			r.With(auth.EnsurePlatformAdminMiddleware).Post("/admin/broadcasts", h.CreateBroadcast)

			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/audit-logs", h.GetGlobalAuditLogs)
			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/roles", h.ListRoles)
			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/companies", h.ListCompanies)
			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/users", h.ListUsers)
			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/gps-devices", h.GetGPSDevices)
			r.With(auth.EnsurePlatformAdminMiddleware).Post("/admin/gps-devices", h.CreateGPSDevice)
			r.With(auth.EnsurePlatformAdminMiddleware).Put("/admin/gps-devices/{imei}/assign", h.AssignGPSDevice)
			r.With(auth.EnsurePlatformAdminMiddleware).Get("/admin/companies/{code}/modules", h.AdminGetTenantModules)
			r.With(auth.EnsurePlatformAdminMiddleware).Post("/admin/companies/{code}/modules", h.AdminUpdateTenantModules)
			r.With(auth.EnsurePlatformAdminMiddleware).Post("/admin/users", h.AdminCreateUser)
			r.With(auth.EnsurePlatformAdminMiddleware).Post("/admin/users/{id}/reset-password", h.AdminResetPassword)
			

			// Tenant endpoints
			r.Get("/gps/available", h.GetAvailableGPSDevices)
			r.Get("/vehicles", h.ListVehicles)
			r.Get("/vehicles/{id}", h.GetVehicle)
			r.Get("/vehicles/{id}/history", h.GetVehicleHistory)
			
					})

		// Websocket endpoint - NO AuthMiddleware here because ServeWS does its own auth (can read query string)
		r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
			ws.ServeWS(hub, w, r, cfg) 
		})
		
		// Fallback for /tracking/live, just alias to ListVehicles which already supports live_state
		r.With(auth.AuthMiddleware(cfg)).Get("/tracking/live", h.ListVehicles)
	})
	
r.Handle("/metrics", promhttp.Handler())

	return r
}
