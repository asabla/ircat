package api

import (
	"encoding/json"
	"testing"
)

func TestAPI_AccountsCRUD(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()

	// Create.
	rec := doJSON(t, h, "POST", "/accounts", token, createAccountRequest{
		Username: "alice",
		Password: "hunter2",
		Email:    "a@example.com",
	})
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created accountRecord
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if created.Username != "alice" || created.Email != "a@example.com" {
		t.Errorf("account = %+v", created)
	}

	// Create conflict.
	rec = doJSON(t, h, "POST", "/accounts", token, createAccountRequest{
		Username: "alice", Password: "x",
	})
	if rec.Code != 409 {
		t.Errorf("dup: %d", rec.Code)
	}

	// List.
	rec = doJSON(t, h, "GET", "/accounts", token, nil)
	if rec.Code != 200 {
		t.Fatalf("list: %d", rec.Code)
	}
	var listResp struct {
		Accounts []accountRecord `json:"accounts"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&listResp)
	if len(listResp.Accounts) != 1 {
		t.Errorf("list = %d accounts", len(listResp.Accounts))
	}

	// Get.
	rec = doJSON(t, h, "GET", "/accounts/alice", token, nil)
	if rec.Code != 200 {
		t.Fatalf("get: %d", rec.Code)
	}

	// Reset password.
	rec = doJSON(t, h, "POST", "/accounts/alice/password", token, resetPasswordRequest{Password: "newpass"})
	if rec.Code != 204 {
		t.Errorf("reset: %d %s", rec.Code, rec.Body.String())
	}

	// Delete.
	rec = doJSON(t, h, "DELETE", "/accounts/alice", token, nil)
	if rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
	rec = doJSON(t, h, "GET", "/accounts/alice", token, nil)
	if rec.Code != 404 {
		t.Errorf("get after delete: %d", rec.Code)
	}
}

func TestAPI_CreateAccount_BadRequest(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()
	rec := doJSON(t, h, "POST", "/accounts", token, createAccountRequest{Username: "only"})
	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestAPI_GetAccount_NotFound(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()
	rec := doJSON(t, h, "GET", "/accounts/ghost", token, nil)
	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestAPI_ChannelRegistrationCRUD(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()

	// Create a founder account first.
	rec := doJSON(t, h, "POST", "/accounts", token, createAccountRequest{
		Username: "founder", Password: "pw",
	})
	if rec.Code != 201 {
		t.Fatalf("acct: %d %s", rec.Code, rec.Body.String())
	}

	// Register #test.
	rec = doJSON(t, h, "POST", "/channels/%23test/registration", token, createRegistrationRequest{
		FounderID: "founder",
		Guard:     true,
	})
	if rec.Code != 201 {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}

	// Get.
	rec = doJSON(t, h, "GET", "/channels/%23test/registration", token, nil)
	if rec.Code != 200 {
		t.Fatalf("get: %d", rec.Code)
	}
	var got registeredChannelRecord
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Channel != "#test" || got.FounderID != "founder" || !got.Guard {
		t.Errorf("reg = %+v", got)
	}

	// Create second account and grant access.
	_ = doJSON(t, h, "POST", "/accounts", token, createAccountRequest{Username: "dave", Password: "pw"})
	rec = doJSON(t, h, "PUT", "/channels/%23test/access/dave", token, setAccessRequest{Flags: "ov"})
	if rec.Code != 200 {
		t.Fatalf("set access: %d %s", rec.Code, rec.Body.String())
	}

	// Get shows access entry.
	rec = doJSON(t, h, "GET", "/channels/%23test/registration", token, nil)
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got.Access) != 1 || got.Access[0].AccountID != "dave" || got.Access[0].Flags != "ov" {
		t.Errorf("access = %+v", got.Access)
	}

	// Update flags (toggle guard off).
	rec = doJSON(t, h, "PUT", "/channels/%23test/registration", token, updateRegistrationRequest{
		FounderID: "founder",
		Guard:     false,
	})
	if rec.Code != 200 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}

	// Revoke access.
	rec = doJSON(t, h, "DELETE", "/channels/%23test/access/dave", token, nil)
	if rec.Code != 204 {
		t.Errorf("del access: %d", rec.Code)
	}

	// Drop registration.
	rec = doJSON(t, h, "DELETE", "/channels/%23test/registration", token, nil)
	if rec.Code != 204 {
		t.Errorf("drop: %d", rec.Code)
	}
	rec = doJSON(t, h, "GET", "/channels/%23test/registration", token, nil)
	if rec.Code != 404 {
		t.Errorf("get after drop: %d", rec.Code)
	}
}

func TestAPI_RegisterChannel_UnknownFounder(t *testing.T) {
	h, token, teardown := newTestAPI(t)
	defer teardown()
	rec := doJSON(t, h, "POST", "/channels/%23test/registration", token, createRegistrationRequest{
		FounderID: "ghost",
	})
	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
}
