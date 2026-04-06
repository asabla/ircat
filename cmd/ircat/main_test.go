package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunHealthcheck_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := runHealthcheck([]string{"--address", srv.URL}); err != nil {
		t.Fatalf("runHealthcheck: %v", err)
	}
}

func TestRunHealthcheck_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := runHealthcheck([]string{"--address", srv.URL})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunHealthcheck_UnreachableIsError(t *testing.T) {
	// Port 1 on localhost is virtually guaranteed to refuse.
	err := runHealthcheck([]string{"--address", "http://127.0.0.1:1/healthz", "--timeout", "200ms"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDispatch_DefaultsToServer_HelpFlag(t *testing.T) {
	// `ircat --help` should reach the server flag set's help and
	// return ErrHelp without panicking.
	err := dispatch([]string{"--help"})
	if err != nil {
		t.Fatalf("dispatch returned %v", err)
	}
}

func TestDispatch_VersionSubcommand(t *testing.T) {
	if err := dispatch([]string{"version"}); err != nil {
		t.Fatalf("dispatch version: %v", err)
	}
}
