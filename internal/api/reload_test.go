package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeReloader records every Reload call so the test can
// assert the endpoint actually fired the hook. The error field
// lets the test exercise both the success and failure paths.
type fakeReloader struct {
	calls atomic.Int32
	err   error
}

func (f *fakeReloader) Reload(_ context.Context) error {
	f.calls.Add(1)
	return f.err
}

func TestAPI_ConfigReload_Success(t *testing.T) {
	h, token, teardown := newTestAPIWithReloader(t, &fakeReloader{})
	defer teardown()

	rec := doJSON(t, h, "POST", "/config/reload", token, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "reloaded") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestAPI_ConfigReload_Failure(t *testing.T) {
	rl := &fakeReloader{err: errors.New("synthetic reload failure")}
	h, token, teardown := newTestAPIWithReloader(t, rl)
	defer teardown()

	rec := doJSON(t, h, "POST", "/config/reload", token, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "synthetic reload failure") {
		t.Errorf("body = %s", rec.Body.String())
	}
	if rl.calls.Load() != 1 {
		t.Errorf("Reload called %d times, want 1", rl.calls.Load())
	}
}

func TestAPI_ConfigReload_NoReloaderReturns503(t *testing.T) {
	// Use the standard newTestAPI which builds without a Reloader.
	h, token, teardown := newTestAPI(t)
	defer teardown()

	rec := doJSON(t, h, "POST", "/config/reload", token, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no_reloader") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestAPI_ConfigReload_RequiresToken(t *testing.T) {
	h, _, teardown := newTestAPIWithReloader(t, &fakeReloader{})
	defer teardown()

	rec := doJSON(t, h, "POST", "/config/reload", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
}
