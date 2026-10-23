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

func TestDeviceIPv4SkipsIPv6(t *testing.T) {
	d := Device{Addresses: []string{"fd7a:115c:a1e0::a028:192f", "100.65.25.45"}}
	if got := d.IPv4(); got != "100.65.25.45" {
		t.Errorf("IPv4 = %q, want 100.65.25.45", got)
	}
	if got := (Device{Addresses: []string{"fd7a:115c:a1e0::1"}}).IPv4(); got != "" {
		t.Errorf("IPv4 of an IPv6-only device = %q, want empty", got)
	}
}

func TestSetIPv4PostsTheAddress(t *testing.T) {
	var gotPath, gotType string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/oauth/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	c, err := New(ctx, srv.URL, "id", "secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.SetIPv4(ctx, "nNEW", "100.65.25.45"); err != nil {
		t.Fatalf("SetIPv4: %v", err)
	}
	if gotPath != "/api/v2/device/nNEW/ip" {
		t.Errorf("path = %q", gotPath)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if gotBody["ipv4"] != "100.65.25.45" {
		t.Errorf("body = %v", gotBody)
	}
}
