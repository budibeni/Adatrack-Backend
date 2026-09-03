package controllers

import (
	"testing"
	"time"

	"ajb_gps/internal"

	"github.com/golang-jwt/jwt/v5"
)

// newTestCfg membangun konfigurasi minimal untuk unit test auth.
func newTestCfg() *internal.Config {
	cfg := &internal.Config{}
	cfg.JWT.Secret = "unit-test-secret"
	cfg.JWT.Expiry = time.Hour
	cfg.RateLimit.LoginMaxAttempts = 5
	cfg.RateLimit.LoginWindow = 15 * time.Minute
	cfg.RateLimit.APIMaxPerMinute = 100
	return cfg
}

// jwtNewExp builds an expiry NumericDate for test tokens.
func jwtNewExp(d time.Duration) *jwt.NumericDate {
	return jwt.NewNumericDate(time.Now().Add(d))
}

// jwtSignForTest signs arbitrary claims with the given secret.
func jwtSignForTest(claims *tokenClaims, secret []byte) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(secret)
	if err != nil {
		return ""
	}
	return signed
}

// TestMain guards that every test starts with the shared test config.
func TestMain(m *testing.M) {
	appCfg = newTestCfg()
	m.Run()
}
