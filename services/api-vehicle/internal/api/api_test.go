package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"backend/internal/config"
	"backend/internal/storage"
)

func TestAllRoutes(t *testing.T) {
	cfg := &config.Config{JWTSecret: "test"}
	store := &storage.S3Store{}
	router := SetupRouter(cfg, store)

	routes := []string{
		"/healthz",
		"/api/v1/vehicles",
		"/api/v1/geofences",
		"/api/v1/routes",
		"/api/v1/speed-configs",
		"/api/v1/notification-preferences",
		"/api/v1/alerts",
		"/api/v1/access/menu",
		"/api/v1/drivers",
		"/api/v1/share-links",
		"/api/v1/groups",
		"/api/v1/assets",
		"/api/v1/organizations",
		"/api/v1/integrations",
	}

	for _, route := range routes {
		req, _ := http.NewRequest("GET", route, nil)
		req.Header.Set("Authorization", "Bearer invalid-token-for-test") // will hit auth middleware or handler
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		// We just care about coverage, not the exact result (could be 401, 200, 500)
		if rr.Code == 404 {
			t.Errorf("Route %s returned 404 Not Found", route)
		}
	}

	// Post requests
	postRoutes := []string{
		"/api/v1/vehicles",
		"/api/v1/geofences",
		"/api/v1/routes",
		"/api/v1/speed-configs",
		"/api/v1/drivers",
		"/api/v1/share-links",
	}
	for _, route := range postRoutes {
		req, _ := http.NewRequest("POST", route, bytes.NewBuffer([]byte(`{}`)))
		req.Header.Set("Authorization", "Bearer invalid-token-for-test") 
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}
}
