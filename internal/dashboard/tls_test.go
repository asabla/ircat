package dashboard

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
)

// generateTestTLSPair writes a self-signed cert/key pair to dir
// and returns the absolute paths. The cert is valid for 127.0.0.1
// and localhost, valid for one hour, and uses an ECDSA P-256 key.
func generateTestTLSPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ircat-dashboard-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// TestDashboard_TLSListener exercises the in-process TLS path:
// the dashboard binds with cfg.Dashboard.TLS.Enabled = true and a
// real cert pair, and a tls.Dialer-backed http.Client hits
// /healthz over HTTPS. The negotiated TLS version must be 1.2 or
// later (the floor we set in dashboard.Run).
func TestDashboard_TLSListener(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateTestTLSPair(t, dir)

	cfg := &config.Config{
		Dashboard: config.DashboardConfig{
			Enabled: true,
			Address: "127.0.0.1:0",
			TLS: config.TLSConfig{
				Enabled:  true,
				CertFile: certPath,
				KeyFile:  keyPath,
			},
		},
	}
	srv := New(Options{Config: cfg})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Wait for the listener to bind.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if a := srv.Addr(); a != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dashboard did not bind in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Drive a TLS request through the listener.
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS12,
			},
		},
	}
	resp, err := client.Get("https://" + srv.Addr() + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Errorf("empty body")
	}
	if resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Errorf("tls version = %v", resp.TLS)
	}
}

// TestDashboard_TLSEnabledMissingCertReturnsError ensures we
// fail closed when an operator sets tls.enabled but forgets the
// cert/key paths. Run binds the listener and immediately
// returns an error rather than serving HTTP on what is
// supposed to be an HTTPS port.
func TestDashboard_TLSEnabledMissingCertReturnsError(t *testing.T) {
	cfg := &config.Config{
		Dashboard: config.DashboardConfig{
			Enabled: true,
			Address: "127.0.0.1:0",
			TLS: config.TLSConfig{
				Enabled: true,
				// CertFile / KeyFile deliberately empty.
			},
		},
	}
	srv := New(Options{Config: cfg})
	err := srv.Run(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
