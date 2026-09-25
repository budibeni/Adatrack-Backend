package controllers

import (
	"net/http"
	"testing"

	"adatrack_gps/api-vehicle/models"
)

// TestResolveProtocol pins the universal-registry contract (PRD Module 1c):
// unknown brands are rejected, and the listener port is derived server-side from
// the registry rather than trusted from the client.
func TestResolveProtocol(t *testing.T) {
	t.Run("unknown protocol rejected", func(t *testing.T) {
		code := "meitrack"
		if _, _, _, apiErr := resolveProtocol(&code, nil); apiErr == nil {
			t.Fatal("an unregistered brand must be rejected with 400")
		} else if apiErr.Code != CodeValidationError || apiErr.Status != http.StatusBadRequest {
			t.Errorf("apiErr = %+v, want 400 VALIDATION_ERROR", apiErr)
		}
	})

	t.Run("brand without protocol rejected", func(t *testing.T) {
		brand := "Concox"
		if _, _, _, apiErr := resolveProtocol(nil, &brand); apiErr == nil {
			t.Fatal("brand without protocol cannot resolve to a listener")
		}
	})

	t.Run("known protocol normalises + derives port", func(t *testing.T) {
		code := "  GT06 "
		got, port, brand, apiErr := resolveProtocol(&code, nil)
		if apiErr != nil {
			t.Fatalf("resolveProtocol: %v", apiErr)
		}
		if got == nil || *got != "gt06" {
			t.Errorf("code = %v, want gt06", got)
		}
		if port == nil || *port != 5001 {
			t.Errorf("port = %v, want 5001 (Traccar convention)", port)
		}
		if brand == nil || *brand != "Concox" {
			t.Errorf("brand = %v, want the registry brand", brand)
		}
	})

	t.Run("absent protocol is allowed", func(t *testing.T) {
		got, port, brand, apiErr := resolveProtocol(nil, nil)
		if apiErr != nil || got != nil || port != nil || brand != nil {
			t.Errorf("absent protocol must stay unknown (got %v/%v/%v, err=%v)", got, port, brand, apiErr)
		}
	})
}

// TestCreateVehicleStoresProtocol proves a cross-brand registration persists the
// registry protocol, its derived port and the brand (B11 task "registrasi device
// lintas brand").
func TestCreateVehicleStoresProtocol(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store)

	c, rec := testContext(http.MethodPost, "/api/v1/vehicles",
		`{"imei":"864201040512345","plate_number":"B 1234 XYZ","protocol":"GT06","device_model":"GT06N"}`,
		adminIdentity())
	svc.handleCreateVehicle(c)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if !store.createdVehicle {
		t.Fatal("the vehicle was not persisted")
	}
	var created *models.Vehicle
	for _, v := range store.vehicles {
		created = v
	}
	if created == nil {
		t.Fatal("no vehicle row captured by the fake store")
	}
	if created.Protocol == nil || *created.Protocol != "gt06" {
		t.Errorf("protocol = %v, want gt06 (normalised)", created.Protocol)
	}
	if created.ProtocolPort == nil || *created.ProtocolPort != 5001 {
		t.Errorf("protocol_port = %v, want the registry port 5001", created.ProtocolPort)
	}
	if created.Brand == nil || *created.Brand != "Concox" {
		t.Errorf("brand = %v, want the registry brand", created.Brand)
	}
}

// TestCreateVehicleRejectsUnknownProtocol: a device must never be registered on a
// listener that does not exist.
func TestCreateVehicleRejectsUnknownProtocol(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store)

	c, rec := testContext(http.MethodPost, "/api/v1/vehicles",
		`{"imei":"864201040512345","plate_number":"B 1234 XYZ","protocol":"meitrack"}`,
		adminIdentity())
	svc.handleCreateVehicle(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create = %d, want 400", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeValidationError {
		t.Errorf("error_code = %s, want %s", code, CodeValidationError)
	}
	if store.createdVehicle {
		t.Error("an unsupported protocol must never reach the store")
	}
}
