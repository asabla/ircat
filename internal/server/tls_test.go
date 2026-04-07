package server

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
)

// generateSelfSignedTLSPair writes a self-signed cert/key pair to
// dir and returns their absolute paths. The cert is valid for
// 127.0.0.1 and the IPv6 loopback so a tls.Dialer with
// ServerName="127.0.0.1" can verify it without InsecureSkipVerify
// (the test still uses InsecureSkipVerify because the cert chain
// is unrooted, but the SAN keeps the file usable for hostname
// verification too).
func generateSelfSignedTLSPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ircat-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  nil,
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

// TestTLSListener_RegistrationOverTLS binds the server on a TLS
// listener with a self-signed cert and drives a full registration
// burst over a TLS dialer. Confirms bindListener wires
// tls.Listen correctly and that the registration path is
// transport-agnostic.
func TestTLSListener_RegistrationOverTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedTLSPair(t, dir)

	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			Name:    "irc.tls.test",
			Network: "TLSNet",
			Listeners: []config.Listener{{
				Address:  "127.0.0.1:0",
				TLS:      true,
				CertFile: certPath,
				KeyFile:  keyPath,
			}},
			Limits: config.LimitsConfig{
				NickLength:              30,
				ChannelLength:           50,
				TopicLength:             390,
				AwayLength:              255,
				KickReasonLength:        255,
				PingIntervalSeconds:     5,
				PingTimeoutSeconds:      20,
				MessageBurst:            100,
				MessageRefillPerSecond:  100,
				MessageViolationsToKick: 5,
			},
		},
	}
	logger, _, err := logging.New(logging.Options{Format: "text", Level: "info"})
	if err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	srv := New(cfg, world, logger)

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
	var addr string
	for {
		if a := srv.ListenerAddrs(); len(a) > 0 {
			addr = a[0].String()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not bind")
		}
		time.Sleep(10 * time.Millisecond)
	}

	dialer := &tls.Dialer{
		Config: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		t.Fatalf("expected *tls.Conn, got %T", conn)
	}
	state := tlsConn.ConnectionState()
	if state.Version < tls.VersionTLS12 {
		t.Errorf("negotiated TLS version too old: %x", state.Version)
	}

	r := bufio.NewReader(conn)
	if _, err := conn.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	expectNumeric(t, conn, r, "001", time.Now().Add(2*time.Second))
}
