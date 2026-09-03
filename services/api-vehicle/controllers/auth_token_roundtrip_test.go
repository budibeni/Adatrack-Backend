package controllers

import (
	"testing"

	"ajb_gps/api-vehicle/models"
)

// TestSignTokenClaimsRoundTrip — HS256 sign → parse → claims terisi (GAP #2 + tenant).
func TestSignTokenClaimsRoundTrip(t *testing.T) {
	orig := appCfg
	defer func() { appCfg = orig }()
	appCfg = newTestCfg()

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", CompanyID: 3, Email: "op@dev001.io"}
	tok, ttl, err := signTokenClaims(mu, "Operator", []int64{11, 12})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if ttl < 3500 || ttl > 3600 {
		t.Errorf("ttl = %ds, dekat 1 jam (3600)", ttl)
	}

	claims, err := parseToken(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.UserID != 7 || claims.CompanyCode != "DEV001" || claims.Email != "op@dev001.io" {
		t.Errorf("claims salah: %+v", claims)
	}
	if claims.Subject != "7" {
		t.Errorf("subject = %q, want 7", claims.Subject)
	}
	if len(claims.VehicleIDs) != 2 || claims.VehicleIDs[0] != 11 {
		t.Errorf("vehicle ids salah: %+v", claims.VehicleIDs)
	}
	if claims.ID == "" {
		t.Error("jti tidak boleh kosong (revocation menjad wajib)")
	}
}

func TestParseTokenRejectsBadSignature(t *testing.T) {
	orig := appCfg
	defer func() { appCfg = orig }()
	appCfg = newTestCfg()

	mu := models.MasterUser{ID: 1, CompanyCode: "DEV001", CompanyID: 1, Email: "a@b.c"}
	tok, _, err := signTokenClaims(mu, "Admin", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Token valid ditandatangani secret lain harus gagal.
	appCfg.JWT.Secret = "different-secret"
	if _, err := parseToken(tok); err == nil {
		t.Fatal("token dgn secret beda tidak boleh valid")
	}
}