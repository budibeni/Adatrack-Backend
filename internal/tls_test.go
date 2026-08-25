// TLS configuration tests (Phase B4 hardening — HTTPS/WSS).
//
// These verify the shared TLS config wiring (env parsing + a real HTTPS
// round-trip to /healthz) WITHOUT requiring MySQL/NATS/Redis, so they run
// in the `internal` module in isolation.
package internal

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genSelfSigned writes a self-signed RSA cert+key to a temp dir and returns
// their paths. Used to exercise ListenAndServeTLS in-process.
func genSelfSigned(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	prk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		DNSNames:     []string{"localhost", "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &prk.PublicKey, prk)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(prk)})

	cf := filepath.Join(dir, "dev.crt")
	kf := filepath.Join(dir, "dev.key")
	if err := os.WriteFile(cf, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kf, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return cf, kf
}

// TestConfigTLSDefaults ensures TLS is off by default (dev stays plain HTTP).
func TestConfigTLSDefaults(t *testing.T) {
	t.Setenv("TLS_ENABLED", "")
	c := LoadConfig()
	if c.HTTP.TLS.Enabled {
		t.Fatal("expected TLS disabled by default")
	}
}

// TestConfigTLSFromEnv ensures env vars flip TLS on and set file paths.
func TestConfigTLSFromEnv(t *testing.T) {
	t.Setenv("TLS_ENABLED", "1")
	t.Setenv("TLS_CERT_FILE", "/tmp/x.crt")
	t.Setenv("TLS_KEY_FILE", "/tmp/x.key")
	c := LoadConfig()
	if !c.HTTP.TLS.Enabled {
		t.Fatal("expected TLS enabled when TLS_ENABLED=1")
	}
	if c.HTTP.TLS.CertFile != "/tmp/x.crt" || c.HTTP.TLS.KeyFile != "/tmp/x.key" {
		t.Fatalf("unexpected cert/key paths: %s/%s", c.HTTP.TLS.CertFile, c.HTTP.TLS.KeyFile)
	}
}

// TestValidateTLSRejectsMalformed ensures Validate errors when TLS enabled
// but a cert/key path is missing. Uses a directly-built Config (getEnv would
// apply file-path defaults) so the guard is exercised deterministically.
func TestValidateTLSRejectsMalformed(t *testing.T) {
	c := &Config{}
	c.Server.Addr = ":1"
	c.TCP.Port = "1"
	c.NATS.URL = "127.0.0.1:1"
	c.MySQL.DSN = "x:y@tcp(127.0.0.1:1)/z"
	c.Redis.Addr = "127.0.0.1:1"
	c.JWT.Secret = "secret"
	c.HTTP.TLS.Enabled = true
	c.HTTP.TLS.CertFile = "" // missing cert path
	c.HTTP.TLS.KeyFile = "/tmp/x.key"
	if err := c.Validate(); err == nil {
		t.Fatal("expected Validate to error when TLS enabled but cert missing")
	}
}

// TestHTTPSServerHealthz stands up a real HTTPS server using TLS config from
// LoadConfig and asserts /healthz returns 200 over TLS.
func TestHTTPSServerHealthz(t *testing.T) {
	cf, kf := genSelfSigned(t)
	t.Setenv("TLS_ENABLED", "true")
	t.Setenv("TLS_CERT_FILE", cf)
	t.Setenv("TLS_KEY_FILE", kf)

	cfg := LoadConfig()
	if !cfg.HTTP.TLS.Enabled || cfg.HTTP.TLS.CertFile != cf || cfg.HTTP.TLS.KeyFile != kf {
		t.Fatalf("TLS config not applied: %+v", cfg.HTTP.TLS)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{
		Addr:              "127.0.0.1:0",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.ServeTLS(ln, cfg.HTTP.TLS.CertFile, cfg.HTTP.TLS.KeyFile) }()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // dev self-signed
		},
		Timeout: 5 * time.Second,
	}
	url := fmt.Sprintf("https://%s/healthz", ln.Addr().String())
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("https GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 over HTTPS, got %d", resp.StatusCode)
	}
	if resp.TLS == nil {
		t.Fatal("expected a TLS-secured response")
	}
	_ = srv.Close()
	<-srvErr
}
