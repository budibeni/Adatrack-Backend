package main

import (
	"context"
	"fmt"
	"time"
	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/dbclient"
	"backend/service-websocket/internal/api"
	"net/http"
	"net/http/httptest"
	"encoding/json"
)

func main() {
	cfg := config.Load()
	dbclient.Connect(context.Background(), cfg)
	defer dbclient.Pool.Close()

	token, _ := auth.GenerateToken(cfg, 1, "superadmin@adatrack.local", "DEFAULT", "SUPER_ADMIN", 1*time.Hour)
	
	req, _ := http.NewRequest("GET", "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	
	rr := httptest.NewRecorder()
	
	handler := api.SetupRouter(cfg, nil)
	handler.ServeHTTP(rr, req)
	
	fmt.Println(rr.Code)
	var out map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &out)
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}
