package tailnet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaleKeepsOnlyDisconnectedTaggedNamesakes(t *testing.T) {
	devices := []Device{
		{NodeID: "old", Hostname: "den", Tags: []string{"tag:den"}},
		{NodeID: "live", Hostname: "den", Tags: []string{"tag:den"}, ConnectedToControl: true},
		{NodeID: "untagged", Hostname: "den"},
		{NodeID: "laptop", Hostname: "mac", Tags: []string{"tag:den"}},
	}
	got := Stale(devices, "den", "tag:den")
	if len(got) != 1 || got[0].NodeID != "old" {
		t.Fatalf("Stale = %+v, want only the disconnected tagged den", got)
	}
}

func TestClientListsAndDeletesWithTheMintedToken(t *testing.T) {
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if r.PostForm.Get("client_id") != "id" || r.PostForm.Get("client_secret") != "secret" {
				http.Error(w, "bad client", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
		case r.Header.Get("Authorization") != "Bearer tok":
			http.Error(w, "no token", http.StatusUnauthorized)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/tailnet/-/devices":
			_, _ = io.WriteString(
				w,
				`{"devices":[{"nodeId":"n1","hostname":"den","tags":["tag:den"],"connectedToControl":false}]}`,
			)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/device/n1":
			deleted = "n1"
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	c, err := New(ctx, srv.URL, "id", "secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	devices, err := c.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devices) != 1 || devices[0].NodeID != "n1" || devices[0].ConnectedToControl {
		t.Fatalf("Devices = %+v", devices)
	}
	if err := c.Delete(ctx, "n1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted != "n1" {
		t.Errorf("deleted %q, want n1", deleted)
	}
}

func TestNewRejectsAMissingClient(t *testing.T) {
	if _, err := New(context.Background(), DefaultBaseURL, "", ""); err == nil {
		t.Fatal("an empty client was accepted")
	}
}
