package controllers

// handlers_commands_test.go — B8 downlink command endpoint (PRD §21.2 row 1).
//
// The suite is hermetic: a fake command store records the audited rows and a fake
// publisher records what would have been sent on `command.request.<company>`, so
// validation, persistence and the dispatch hand-off are all asserted without
// PostgreSQL or NATS.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// fakeCommandStore implements only the command surface (+ the vehicle lookup the
// handler performs). The embedded Store interface panics on anything else, which
// keeps the test honest about what the handler touched.
type fakeCommandStore struct {
	Store
	vehicles map[int64]*models.Vehicle
	commands []*models.DeviceCommand
	created  bool
	listN    int64
}

func (f *fakeCommandStore) VehicleByID(_ context.Context, _ string, id int64, _ bool) (*models.Vehicle, error) {
	if v, ok := f.vehicles[id]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeCommandStore) CreateDeviceCommand(_ context.Context, _ string, cmd *models.DeviceCommand) (int64, error) {
	f.created = true
	cmd.ID = int64(len(f.commands) + 1)
	f.commands = append(f.commands, cmd)
	return cmd.ID, nil
}

func (f *fakeCommandStore) DeviceCommandByRequestID(_ context.Context, _, requestID string) (*models.DeviceCommand, error) {
	for _, c := range f.commands {
		if c.RequestID == requestID {
			return c, nil
		}
	}
	return nil, nil
}

func (f *fakeCommandStore) ListDeviceCommands(_ context.Context, _ CommandQuery) ([]models.DeviceCommand, int64, error) {
	out := make([]models.DeviceCommand, 0, len(f.commands))
	for _, c := range f.commands {
		out = append(out, *c)
	}
	return out, f.listN, nil
}

// fakePublisher records the published requests.
type fakePublisher struct {
	published []*models.DeviceCommand
	companies []string
	err       error
}

func (f *fakePublisher) PublishDeviceCommand(company string, cmd *models.DeviceCommand) error {
	if f.err != nil {
		return f.err
	}
	f.companies = append(f.companies, company)
	f.published = append(f.published, cmd)
	return nil
}

// newCommandTestService wires the handler with the fake store + publisher.
func newCommandTestService(store *fakeCommandStore, pub CommandPublisher) *Service {
	return NewService(Deps{
		Settings: Settings{
			HTTPAddr:        ":0",
			JWTSecret:       strings.Repeat("t", 40),
			JWTIssuer:       "test",
			DefaultPageSize: 100,
			MaxPageSize:     1000,
		},
		Store:    store,
		Commands: pub,
	})
}

// commandContext builds a request context with the identity and the `:id` path
// parameter attached (the handlers read it through pathID).
func commandContext(method, target, body string, identity *tenantIdentity, vehicleID string) (*gin.Context, *httptest.ResponseRecorder) {
	c, rec := testContext(method, target, body, identity)
	c.Params = gin.Params{{Key: "id", Value: vehicleID}}
	return c, rec
}

func TestCreateVehicleCommandHappyPath(t *testing.T) {
	store := &fakeCommandStore{vehicles: map[int64]*models.Vehicle{
		1: {ID: 1, IMEI: "123456789012345", Status: "active"},
	}}
	pub := &fakePublisher{}
	svc := newCommandTestService(store, pub)

	c, rec := commandContext(http.MethodPost, "/api/v1/vehicles/1/commands",
		`{"command":"engine_cut"}`, adminIdentity(), "1")
	svc.handleCreateVehicleCommand(c)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if !store.created || len(store.commands) != 1 {
		t.Fatalf("audited rows = %d, want 1", len(store.commands))
	}
	row := store.commands[0]
	if row.RequestID == "" || row.IMEI != "123456789012345" || row.Command != models.CommandEngineCut {
		t.Fatalf("audited row = %+v", row)
	}
	if len(pub.published) != 1 || pub.companies[0] != "DEV001" {
		t.Fatalf("publisher calls = %d (companies %v), want 1 for DEV001", len(pub.published), pub.companies)
	}
	if pub.published[0].RequestID != row.RequestID {
		t.Fatal("published request_id differs from the audited row")
	}

	var body struct {
		Data models.DeviceCommand `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the success envelope: %v", err)
	}
	if body.Data.Status != models.CommandPending || body.Data.ID == 0 {
		t.Fatalf("response command = %+v, want the pending stored row", body.Data)
	}
}

func TestCreateVehicleCommandValidation(t *testing.T) {
	store := &fakeCommandStore{vehicles: map[int64]*models.Vehicle{
		1: {ID: 1, IMEI: "123456789012345", Status: "active"},
	}}
	pub := &fakePublisher{}
	svc := newCommandTestService(store, pub)

	cases := []struct {
		name string
		body string
	}{
		{"unknown command", `{"command":"open_trunk"}`},
		{"empty command", `{}`},
		{"interval too small", `{"command":"set_interval","interval_seconds":1}`},
		{"interval too large", `{"command":"set_interval","interval_seconds":100000}`},
	}
	for _, tc := range cases {
		c, rec := commandContext(http.MethodPost, "/api/v1/vehicles/1/commands", tc.body, adminIdentity(), "1")
		svc.handleCreateVehicleCommand(c)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), CodeValidationError) {
			t.Errorf("%s: body = %s, want VALIDATION_ERROR", tc.name, rec.Body.String())
		}
		if store.created {
			t.Errorf("%s: an invalid command was persisted", tc.name)
		}
		if len(pub.published) != 0 {
			t.Errorf("%s: an invalid command was published", tc.name)
		}
	}
}

func TestCreateVehicleCommandRequiresDispatch(t *testing.T) {
	store := &fakeCommandStore{vehicles: map[int64]*models.Vehicle{
		1: {ID: 1, IMEI: "123456789012345", Status: "active"},
	}}
	svc := newCommandTestService(store, nil) // no publisher wired

	c, rec := commandContext(http.MethodPost, "/api/v1/vehicles/1/commands", `{"command":"locate"}`, adminIdentity(), "1")
	svc.handleCreateVehicleCommand(c)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when dispatch is unavailable", rec.Code)
	}
}

func TestCreateVehicleCommandRejectsInactiveVehicle(t *testing.T) {
	store := &fakeCommandStore{vehicles: map[int64]*models.Vehicle{
		1: {ID: 1, IMEI: "123456789012345", Status: "inactive"},
	}}
	pub := &fakePublisher{}
	svc := newCommandTestService(store, pub)

	c, rec := commandContext(http.MethodPost, "/api/v1/vehicles/1/commands", `{"command":"reboot"}`, adminIdentity(), "1")
	svc.handleCreateVehicleCommand(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for an inactive vehicle", rec.Code)
	}
	if len(pub.published) != 0 {
		t.Fatal("a command was dispatched to an inactive vehicle")
	}
}

func TestListVehicleCommands(t *testing.T) {
	store := &fakeCommandStore{
		vehicles: map[int64]*models.Vehicle{1: {ID: 1, IMEI: "123456789012345", Status: "active"}},
		commands: []*models.DeviceCommand{
			{ID: 1, RequestID: "r1", VehicleID: 1, Command: models.CommandEngineCut, Status: models.CommandAcked},
		},
		listN: 1,
	}
	svc := newCommandTestService(store, &fakePublisher{})

	c, rec := commandContext(http.MethodGet, "/api/v1/vehicles/1/commands", "", adminIdentity(), "1")
	svc.handleListVehicleCommands(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"request_id":"r1"`) {
		t.Fatalf("body = %s, want the audited command", rec.Body.String())
	}

	// An unknown status filter is a validation error, not an empty page.
	c, rec = commandContext(http.MethodGet, "/api/v1/vehicles/1/commands?status=weird", "", adminIdentity(), "1")
	svc.handleListVehicleCommands(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status filter: code = %d, want 400", rec.Code)
	}
}
