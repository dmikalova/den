package gcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testKey builds a service account key file around a throwaway RSA key, so
// the signing path is exercised rather than stubbed.
func testKey(t *testing.T, tokenURI string) []byte {
	t.Helper()

	// 2048 is the smallest size Go will sign with and keeps the test fast.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshalling the key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	b, err := json.Marshal(Key{
		ClientEmail:  "den-host@mklv-infrastructure.iam.gserviceaccount.com",
		PrivateKey:   string(pemKey),
		PrivateKeyID: "abc123",
		ProjectID:    "mklv-infrastructure",
		TokenURI:     tokenURI,
	})
	if err != nil {
		t.Fatalf("marshalling the key file: %v", err)
	}
	return b
}

func TestSecretReadsJSONObject(t *testing.T) {
	var gotAuth, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parsing the token form: %v", err)
			}
			if got := r.Form.Get(
				"grant_type",
			); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Errorf("grant_type = %q, want the jwt-bearer grant", got)
			}
			if parts := strings.Split(r.Form.Get("assertion"), "."); len(parts) != 3 {
				t.Errorf("assertion has %d segments, want 3", len(parts))
			}
			_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
			return
		}

		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		payload := base64.StdEncoding.EncodeToString([]byte(`{"AUTHKEY":"tskey-abc"}`))
		_, _ = io.WriteString(w, `{"payload":{"data":"`+payload+`"}}`)
	}))
	defer srv.Close()

	c, err := New(testKey(t, srv.URL+"/token"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Point the client at the stub by rewriting the request host, so the
	// production URL constants are the ones under test.
	c.http = srv.Client()
	c.http.Transport = rewriteHost{to: srv.URL, next: http.DefaultTransport}

	data, err := c.Secret(context.Background(), "tailscale-den")
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if data["AUTHKEY"] != "tskey-abc" {
		t.Errorf("AUTHKEY = %q, want tskey-abc", data["AUTHKEY"])
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", gotAuth)
	}
	if want := "/v1/projects/mklv-infrastructure/secrets/tailscale-den/versions/latest:access"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// rewriteHost sends every request to the test server, so the production URL
// constants are the ones under test.
type rewriteHost struct {
	to   string
	next http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	to, err := http.NewRequestWithContext(req.Context(), req.Method, r.to+req.URL.Path, req.Body)
	if err != nil {
		return nil, err
	}
	to.Header = req.Header
	if req.URL.Path == "/token" {
		to.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return r.next.RoundTrip(to)
}

func TestSecretRejectsABareString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
			return
		}
		payload := base64.StdEncoding.EncodeToString([]byte("tskey-abc"))
		_, _ = io.WriteString(w, `{"payload":{"data":"`+payload+`"}}`)
	}))
	defer srv.Close()

	c, err := New(testKey(t, srv.URL+"/token"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.http = srv.Client()
	c.http.Transport = rewriteHost{to: srv.URL, next: http.DefaultTransport}

	if _, err := c.Secret(context.Background(), "legacy"); err == nil {
		t.Fatal("want an error for a bare-string secret, got nil")
	}
}

func TestNewRejectsAnIncompleteKey(t *testing.T) {
	for name, keyJSON := range map[string]string{
		"not JSON":       `nope`,
		"no email":       `{"private_key":"x","project_id":"p"}`,
		"no private key": `{"client_email":"a@b","project_id":"p"}`,
		"no project":     `{"client_email":"a@b","private_key":"x"}`,
		"bad PEM":        `{"client_email":"a@b","private_key":"x","project_id":"p"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New([]byte(keyJSON)); err == nil {
				t.Fatal("want an error, got nil")
			}
		})
	}
}

func TestErrorsDoNotCarryTheKey(t *testing.T) {
	keyJSON := testKey(t, "https://oauth2.googleapis.com/token")
	var k Key
	if err := json.Unmarshal(keyJSON, &k); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}

	// A truncated key file still parses as JSON, so this exercises the
	// signing path's error rather than the shape checks above.
	broken, err := json.Marshal(Key{
		ClientEmail: k.ClientEmail,
		PrivateKey:  "-----BEGIN PRIVATE KEY-----\nbm90YWtleQ==\n-----END PRIVATE KEY-----\n",
		ProjectID:   k.ProjectID,
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	_, err = New(broken)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if strings.Contains(err.Error(), "bm90YWtleQ") {
		t.Errorf("the error quotes the key material: %v", err)
	}
}

func TestJSONRoundTrips(t *testing.T) {
	want := map[string]string{"A": "1", "B": "2"}
	raw, err := JSON(want)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != len(want) || got["A"] != "1" || got["B"] != "2" {
		t.Errorf("round trip = %v, want %v", got, want)
	}
}
