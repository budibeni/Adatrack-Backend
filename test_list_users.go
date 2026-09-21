package main

import (
	"fmt"
	"time"
	"backend/internal/auth"
	"backend/internal/config"
	"net/http"
	"net/http/httptest"
	"backend/internal/dbclient"
	"backend/internal/api"
)

func main() {
	cfg := config.LoadConfig()
	dbclient.InitDB(cfg)
	defer dbclient.CloseDB()
	
	token, _ := auth.GenerateToken(cfg, 1, "superadmin@adatrack.local", "DEFAULT", "SUPER_ADMIN", 1*time.Hour)
	
	req, _ := http.NewRequest("GET", "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	
	rr := httptest.NewRecorder()
	
	// mock router
	handler := api.SetupRouter(cfg, nil)
	handler.ServeHTTP(rr, req)
	
	fmt.Println(rr.Code)
	fmt.Println(rr.Body.String())
}
