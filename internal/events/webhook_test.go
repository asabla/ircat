package events

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookSink_HappyPath(t *testing.T) {
	var received []Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev Event
		if err := json.Unmarshal(body, &ev); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		received = append(received, ev)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sink, err := NewWebhookSink(WebhookSinkOptions{
		URL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	if err := sink.Handle(context.Background(), Event{ID: "1", Type: "oper_up"}); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].ID != "1" {
		t.Errorf("received = %+v", received)
	}
}

func TestWebhookSink_HMACSignature(t *testing.T) {
	var sawSig string
	var sawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSig = r.Header.Get("X-Ircat-Signature")
		sawBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	secret := "topsecret"
	sink, _ := NewWebhookSink(WebhookSinkOptions{URL: srv.URL, Secret: secret})
	defer sink.Close()
	_ = sink.Handle(context.Background(), Event{ID: "1", Type: "oper_up"})

	if !strings.HasPrefix(sawSig, "sha256=") {
		t.Fatalf("signature missing or malformed: %q", sawSig)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(sawBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sawSig != want {
		t.Errorf("sig = %q, want %q", sawSig, want)
	}
}

func TestWebhookSink_RetriesOn5xxThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sink, _ := NewWebhookSink(WebhookSinkOptions{
		URL:        srv.URL,
		MaxRetries: 5,
		Backoff:    func(int) time.Duration { return 10 * time.Millisecond },
	})
	defer sink.Close()

	if err := sink.Handle(context.Background(), Event{ID: "1"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d", attempts.Load())
	}
}

func TestWebhookSink_4xxIsPermanent(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(400)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dlq := filepath.Join(dir, "dlq.jsonl")
	sink, _ := NewWebhookSink(WebhookSinkOptions{
		URL:            srv.URL,
		MaxRetries:     5,
		Backoff:        func(int) time.Duration { return 10 * time.Millisecond },
		DeadLetterPath: dlq,
	})
	defer sink.Close()

	// 4xx surfaces as "no retry, straight to DLQ". Handle returns
	// nil because the event reached its final destination.
	if err := sink.Handle(context.Background(), Event{ID: "1"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (no retries on 4xx)", attempts.Load())
	}
	// DLQ file should contain the event.
	data, err := os.ReadFile(dlq)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"id":"1"`) {
		t.Errorf("dlq = %s", data)
	}
}

func TestWebhookSink_DLQOnExhaustedRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dlq := filepath.Join(dir, "dlq.jsonl")
	sink, _ := NewWebhookSink(WebhookSinkOptions{
		URL:            srv.URL,
		MaxRetries:     2,
		Backoff:        func(int) time.Duration { return 5 * time.Millisecond },
		DeadLetterPath: dlq,
	})
	defer sink.Close()

	_ = sink.Handle(context.Background(), Event{ID: "1"})
	// Verify the DLQ file has the event line.
	f, err := os.Open(dlq)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	var lines []string
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	if len(lines) != 1 {
		t.Fatalf("dlq lines = %d", len(lines))
	}
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.ID != "1" {
		t.Errorf("dlq event = %+v", ev)
	}
}
