package events

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WebhookSinkOptions configures [NewWebhookSink].
type WebhookSinkOptions struct {
	// URL is the endpoint to POST events to. Required.
	URL string
	// Secret, when non-empty, is used to sign each request with
	// HMAC-SHA256; the signature lands in the X-Ircat-Signature
	// header as "sha256=<hex>". The consumer verifies by
	// recomputing the HMAC over the request body.
	Secret string
	// Timeout bounds a single HTTP attempt. Defaults to 5s.
	Timeout time.Duration
	// Retry attempts after the first send failure. Defaults to 5.
	MaxRetries int
	// Backoff returns the delay to wait before attempt N (zero-
	// indexed). Nil means DefaultBackoff.
	Backoff func(attempt int) time.Duration
	// DeadLetterPath is the file to append events to when retries
	// exhaust. Empty disables DLQ (events are logged and dropped).
	DeadLetterPath string
}

// DefaultBackoff returns an exponential backoff schedule starting
// at 1 second and doubling each attempt, capped at 60s.
func DefaultBackoff(attempt int) time.Duration {
	d := time.Second << attempt
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// WebhookSink POSTs events to an HTTP endpoint one at a time with
// retry + exponential backoff. On permanent failure (all retries
// exhausted) the event is appended to a dead-letter JSONL file.
//
// M6 deliberately keeps this simple: one event per POST, sequential
// sends inside the subscriber goroutine. Batching is a M6 follow-up
// once we have a real workload to tune against.
type WebhookSink struct {
	opts   WebhookSinkOptions
	client *http.Client

	dlqMu sync.Mutex
	dlq   *os.File
}

// NewWebhookSink constructs the sink. It opens the DLQ file eagerly
// so configuration errors surface at startup.
func NewWebhookSink(opts WebhookSinkOptions) (*WebhookSink, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("events: webhook: url is required")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 5
	}
	if opts.Backoff == nil {
		opts.Backoff = DefaultBackoff
	}
	s := &WebhookSink{
		opts: opts,
		client: &http.Client{
			Timeout: opts.Timeout,
		},
	}
	if opts.DeadLetterPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.DeadLetterPath), 0o755); err != nil {
			return nil, fmt.Errorf("events: webhook dlq mkdir: %w", err)
		}
		f, err := os.OpenFile(opts.DeadLetterPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return nil, fmt.Errorf("events: webhook dlq open: %w", err)
		}
		s.dlq = f
	}
	return s, nil
}

// Name implements [Sink].
func (s *WebhookSink) Name() string { return "webhook" }

// Handle implements [Sink]. POSTs the event as JSON, retries with
// backoff on transport errors or 5xx responses, and finally appends
// to the DLQ if every attempt fails.
func (s *WebhookSink) Handle(ctx context.Context, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrInvalidEvent, err)
	}
	for attempt := 0; attempt <= s.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := s.opts.Backoff(attempt - 1)
			select {
			case <-ctx.Done():
				return s.writeDLQ(ev)
			case <-time.After(delay):
			}
		}
		ok, perm := s.attempt(ctx, body)
		if ok {
			return nil
		}
		if perm {
			// Permanent client error (4xx) — retrying will not help.
			break
		}
	}
	return s.writeDLQ(ev)
}

// attempt returns (ok, permanent). ok=true means the request
// succeeded; permanent=true means do not retry (4xx response or
// context error).
func (s *WebhookSink) attempt(ctx context.Context, body []byte) (ok, permanent bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.URL, bytes.NewReader(body))
	if err != nil {
		return false, true
	}
	req.Header.Set("Content-Type", "application/json")
	if s.opts.Secret != "" {
		mac := hmac.New(sha256.New, []byte(s.opts.Secret))
		mac.Write(body)
		req.Header.Set("X-Ircat-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return true, false
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return false, true // 4xx: do not retry
	default:
		return false, false // 5xx / other: retry
	}
}

// writeDLQ appends the event to the dead-letter file (or returns an
// error when no DLQ is configured).
func (s *WebhookSink) writeDLQ(ev Event) error {
	s.dlqMu.Lock()
	defer s.dlqMu.Unlock()
	if s.dlq == nil {
		return fmt.Errorf("events: webhook: all retries failed and no DLQ configured")
	}
	buf, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	if _, err := s.dlq.Write(buf); err != nil {
		return fmt.Errorf("events: webhook dlq write: %w", err)
	}
	return nil
}

// Close implements [Sink].
func (s *WebhookSink) Close() error {
	s.dlqMu.Lock()
	defer s.dlqMu.Unlock()
	if s.dlq == nil {
		return nil
	}
	err := s.dlq.Close()
	s.dlq = nil
	return err
}
