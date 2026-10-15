package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"backend/internal/auth"
	"backend/internal/dbclient"
	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
)

type MobileLocationPoint struct {
	Lat          float64  `json:"lat"`
	Lon          float64  `json:"lon"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	Speed        float64  `json:"speed"`
	Heading      float64  `json:"heading"`
	Altitude     float64  `json:"altitude"`
	Accuracy     float64  `json:"accuracy"`
	BatteryLevel float64  `json:"battery_level"`
	IsCharging   bool     `json:"is_charging"`
	SOS          bool     `json:"sos"`
	Timestamp    int64    `json:"timestamp"`
}

type MobileTrackingRequest struct {
	DeviceID  string                `json:"device_id"`
	Token     *string               `json:"token,omitempty"`
	Locations []MobileLocationPoint `json:"locations,omitempty"`

	// Flat single-point fields fallback
	Lat          *float64 `json:"lat,omitempty"`
	Lon          *float64 `json:"lon,omitempty"`
	Latitude     *float64 `json:"latitude,omitempty"`
	Longitude    *float64 `json:"longitude,omitempty"`
	Speed        *float64 `json:"speed,omitempty"`
	Heading      *float64 `json:"heading,omitempty"`
	Altitude     *float64 `json:"altitude,omitempty"`
	Accuracy     *float64 `json:"accuracy,omitempty"`
	BatteryLevel *float64 `json:"battery_level,omitempty"`
	IsCharging   *bool    `json:"is_charging,omitempty"`
	SOS          *bool    `json:"sos,omitempty"`
	Timestamp    *int64   `json:"timestamp,omitempty"`
}

type MobileTrackingResponse struct {
	Success         bool   `json:"success"`
	Message         string `json:"message"`
	ProcessedPoints int    `json:"processed_points"`
	ServerTime      int64  `json:"server_time"`
}

func (h *Handler) writeMobileError(w http.ResponseWriter, status int, errorCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    false,
		"status":     "error",
		"error_code": errorCode,
		"message":    msg,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleMobileTracking receives GPS coordinates from smartphone tracker applications
func (h *Handler) HandleMobileTracking(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MobileTrackingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeMobileError(w, http.StatusBadRequest, "INVALID_PAYLOAD", "Malformed JSON body: "+err.Error())
		return
	}

	// 1. Mandatory Token Authentication (Token MUST always be provided)
	token := r.Header.Get("X-Device-Token")
	if token == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else if authHeader != "" {
			token = authHeader
		}
	}
	if token == "" && req.Token != nil {
		token = *req.Token
	}

	if strings.TrimSpace(token) == "" {
		h.writeMobileError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token autentikasi wajib disertakan (gunakan header 'Authorization: Bearer <token>' atau 'X-Device-Token: <token>')")
		return
	}

	// Validate token against pre-shared secret or valid JWT token
	defaultMobileSecret := os.Getenv("MOBILE_DEVICE_TOKEN")
	if defaultMobileSecret == "" {
		defaultMobileSecret = "adatrack-mobile-secret-2026"
	}

	isValid := false
	if token == defaultMobileSecret {
		isValid = true
	} else if h.cfg != nil {
		if claims, errJwt := auth.ValidateToken(h.cfg, token); errJwt == nil && claims != nil {
			isValid = true
		}
	}

	if !isValid {
		h.writeMobileError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token autentikasi tidak valid atau sudah kedaluwarsa")
		return
	}

	if req.DeviceID == "" {
		h.writeMobileError(w, http.StatusBadRequest, "MISSING_DEVICE_ID", "device_id is required")
		return
	}

	if req.Lat == nil && req.Latitude != nil {
		req.Lat = req.Latitude
	}
	if req.Lon == nil && req.Longitude != nil {
		req.Lon = req.Longitude
	}

	// Consolidate points (handle both single-point flat JSON and batch array JSON)
	points := req.Locations
	for i := range points {
		if points[i].Lat == 0 && points[i].Latitude != nil {
			points[i].Lat = *points[i].Latitude
		}
		if points[i].Lon == 0 && points[i].Longitude != nil {
			points[i].Lon = *points[i].Longitude
		}
	}

	if len(points) == 0 && req.Lat != nil && req.Lon != nil {
		ts := time.Now().Unix()
		if req.Timestamp != nil && *req.Timestamp > 0 {
			ts = *req.Timestamp
		}

		speed := 0.0
		if req.Speed != nil {
			speed = *req.Speed
		}
		heading := 0.0
		if req.Heading != nil {
			heading = *req.Heading
		}
		altitude := 0.0
		if req.Altitude != nil {
			altitude = *req.Altitude
		}
		accuracy := 0.0
		if req.Accuracy != nil {
			accuracy = *req.Accuracy
		}
		battery := 100.0
		if req.BatteryLevel != nil {
			battery = *req.BatteryLevel
		}
		charging := false
		if req.IsCharging != nil {
			charging = *req.IsCharging
		}
		sos := false
		if req.SOS != nil {
			sos = *req.SOS
		}

		points = append(points, MobileLocationPoint{
			Lat:          *req.Lat,
			Lon:          *req.Lon,
			Speed:        speed,
			Heading:      heading,
			Altitude:     altitude,
			Accuracy:     accuracy,
			BatteryLevel: battery,
			IsCharging:   charging,
			SOS:          sos,
			Timestamp:    ts,
		})
	}

	if len(points) == 0 {
		h.writeMobileError(w, http.StatusBadRequest, "NO_LOCATION_DATA", "At least one location point (lat, lon) is required")
		return
	}

	// Resolve device_id to company_code and vehicle_id
	var companyCode string
	var vehicleID int

	// 1. Check tm_vehicle_imei_map first
	err := dbclient.Pool.QueryRow(r.Context(),
		"SELECT company_code, vehicle_id FROM adatrack_gps_master.tm_vehicle_imei_map WHERE imei = $1",
		req.DeviceID,
	).Scan(&companyCode, &vehicleID)

	if err != nil {
		// 2. Check tm_gps_devices if not in vehicle_imei_map
		var assignedCompany *string
		errDev := dbclient.Pool.QueryRow(r.Context(),
			"SELECT assigned_company FROM adatrack_gps_master.tm_gps_devices WHERE imei = $1",
			req.DeviceID,
		).Scan(&assignedCompany)

		if errDev != nil {
			h.writeMobileError(w, http.StatusNotFound, "DEVICE_NOT_REGISTERED",
				fmt.Sprintf("Device ID '%s' is not registered in Adatrack master devices", req.DeviceID))
			return
		}

		if assignedCompany != nil && *assignedCompany != "" {
			companyCode = *assignedCompany
		} else {
			companyCode = "DEFAULT"
		}
	}

	processed := 0
	for _, pt := range points {
		// Sanity check lat/lon
		if pt.Lat < -90 || pt.Lat > 90 || pt.Lon < -180 || pt.Lon > 180 {
			continue
		}

		// Normalize timestamp
		var t time.Time
		if pt.Timestamp > 1000000000000 { // Milliseconds
			t = time.UnixMilli(pt.Timestamp).UTC()
		} else if pt.Timestamp > 0 { // Seconds
			t = time.Unix(pt.Timestamp, 0).UTC()
		} else {
			t = time.Now().UTC()
		}

		acc := int16(0)
		if pt.IsCharging || pt.Speed > 2.0 {
			acc = 1
		}

		payload := models.TelemetryPayload{
			IMEI:        req.DeviceID,
			CompanyCode: companyCode,
			VehicleID:   vehicleID,
			Latitude:    pt.Lat,
			Longitude:   pt.Lon,
			Speed:       pt.Speed,
			Heading:     pt.Heading,
			Altitude:    pt.Altitude,
			ACCStatus:   acc,
			Battery:     pt.BatteryLevel,
			Satellites:  8,
			GSMSignal:   100,
			Timestamp:   t,
			Status:      "ONLINE",
		}

		if pt.SOS {
			payload.EventCode = 1 // Emergency / SOS Alert
		}

		data, err := json.Marshal(payload)
		if err != nil {
			continue
		}

		subject := fmt.Sprintf("telemetry.raw.%s", req.DeviceID)
		if natsclient.JS != nil {
			if _, errPub := natsclient.JS.Publish(subject, data); errPub != nil {
				logger.Log.Error("Failed to publish mobile telemetry to NATS", "device_id", req.DeviceID, "err", errPub)
			} else {
				processed++
			}
		} else {
			logger.Log.Warn("NATS JetStream not connected, skipping mobile telemetry publish")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(MobileTrackingResponse{
		Success:         true,
		Message:         "Telemetry accepted",
		ProcessedPoints: processed,
		ServerTime:      time.Now().Unix(),
	})
}
