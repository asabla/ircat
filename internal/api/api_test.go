package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// newTestAPI builds an API with a fresh sqlite store and a single
// minted token. Returns the API handler, the token plaintext, and a
// teardown.
func newTestAPI(t *testing.T) (http.Handler, string, func()) {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlite.Open(filepath.Join(dir, "ircat.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tok, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.APITokens().Create(context.Background(), &storage.APIToken{
		ID:    tok.ID,
		Label: "test",
		Hash:  tok.Hash,
	}); err != nil {
		t.Fatal(err)
	}

	world := state.NewWorld()
	api := New(Options{
		Store: store,
		World: world,
	})
	teardown := func() { _ = store.Close() }
	return api.Handler(), tok.Plaintext, teardown
}

func doJSON(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPI_TokenMiddleware_Unauthorized(t *testing.T) {
	h, _, teardown := newTestAPI(t)
	defer teardown()

	rec := doJSON(t, h, "GET", "/server", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing_token") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestAPI_TokenMiddleware_BadToken(t *testing.T) {
	h, _, teardown := newTestAPI(t)
	defer teardown()
	rec := doJSON(t, h, "GET", "/server", "ircat_doesnotexist", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestAPI_GetServer(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()
	rec := doJSON(t, h, "GET", "/server", token, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp serverInfoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	// users + channels both default to 0; the test does not wire
	// up an Actuator so listeners is empty.
	if resp.Users != 0 || resp.Channels != 0 {
		t.Errorf("counts = %d / %d", resp.Users, resp.Channels)
	}
}

func TestAPI_OperatorsCRUD(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()

	// Create.
	rec := doJSON(t, h, "POST", "/operators", token, createOperatorRequest{
		Name:     "alice",
		Password: "secret",
		HostMask: "*@10.0.0.*",
		Flags:    []string{"kill"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	// List.
	rec = doJSON(t, h, "GET", "/operators", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var listResp struct {
		Operators []operatorRecord `json:"operators"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Operators) != 1 || listResp.Operators[0].Name != "alice" {
		t.Errorf("list = %#v", listResp.Operators)
	}

	// Get.
	rec = doJSON(t, h, "GET", "/operators/alice", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}

	// Get missing.
	rec = doJSON(t, h, "GET", "/operators/ghost", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get missing: %d", rec.Code)
	}

	// Conflict.
	rec = doJSON(t, h, "POST", "/operators", token, createOperatorRequest{
		Name:     "alice",
		Password: "second",
	})
	if rec.Code != http.StatusConflict {
		t.Errorf("dup create: %d", rec.Code)
	}

	// Delete.
	rec = doJSON(t, h, "DELETE", "/operators/alice", token, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
	rec = doJSON(t, h, "GET", "/operators/alice", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get after delete: %d", rec.Code)
	}
}

func TestAPI_CreateOperator_BadJSON(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()
	req := httptest.NewRequest("POST", "/operators", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestAPI_CreateOperator_MissingFields(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()
	rec := doJSON(t, h, "POST", "/operators", token, createOperatorRequest{Name: "no-password"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}
