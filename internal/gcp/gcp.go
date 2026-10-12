// Package gcp reads GCP Secret Manager with a service account key, over HTTP,
// without gcloud and without the Google SDK.
//
// den makes one kind of call with one credential. The SDK would bring
// credential discovery with it, which searches the environment, the metadata
// server and the gcloud config, and so widens what den can authenticate as
// beyond the key it was given. gcloud is worse again: it is a Python runtime
// that would have to be installed and kept current on the host.
package gcp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// scope is the only scope den asks for. Secret Manager has no narrower one:
// cloud-platform is what the accessor role is checked against, and the role
// is what actually bounds this key to one secret.
const scope = "https://www.googleapis.com/auth/cloud-platform"

const secretManagerAPI = "https://secretmanager.googleapis.com/v1"

// maxResponse bounds a reply from an endpoint that should return a few
// kilobytes, so a wrong or hostile URL cannot exhaust memory.
const maxResponse = 1 << 20

// Key is the half of a service account key file that den uses. The remaining
// fields are either constant or unused, and listing them would imply den
// cares what they say.
type Key struct {
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	ProjectID    string `json:"project_id"`
	TokenURI     string `json:"token_uri"`
}

// Client fetches secrets as one service account.
type Client struct {
	key    Key
	signer *rsa.PrivateKey
	http   *http.Client

	token  string
	expiry time.Time
}

// New parses a service account key file. The key never leaves this process
// and is not logged, so errors describe the shape of the file and never its
// contents.
func New(keyJSON []byte) (*Client, error) {
	var k Key
	if err := json.Unmarshal(keyJSON, &k); err != nil {
		return nil, fmt.Errorf("the service account key is not JSON: %w", err)
	}
	switch {
	case k.ClientEmail == "":
		return nil, fmt.Errorf("the service account key has no client_email")
	case k.PrivateKey == "":
		return nil, fmt.Errorf("the service account key has no private_key")
	case k.ProjectID == "":
		return nil, fmt.Errorf("the service account key has no project_id")
	}
	if k.TokenURI == "" {
		k.TokenURI = "https://oauth2.googleapis.com/token"
	}

	signer, err := parsePrivateKey(k.PrivateKey)
	if err != nil {
		return nil, err
	}

	return &Client{
		key:    k,
		signer: signer,
		http:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Project is the project the key was issued in. den never needs to name it
// separately, which is one less thing to keep in step.
func (c *Client) Project() string { return c.key.ProjectID }

// Secret returns the newest version of a Secret Manager secret, decoded as a
// JSON object. Secrets in this project hold an object rather than a bare
// value so that one grant covers everything a reader needs.
func (c *Client) Secret(ctx context.Context, name string) (map[string]string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf(
		"%s/projects/%s/secrets/%s/versions/latest:access",
		secretManagerAPI, url.PathEscape(c.key.ProjectID), url.PathEscape(name),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("reading secret %s: %w", name, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	body, err := c.do(req, fmt.Sprintf("reading secret %s", name))
	if err != nil {
		return nil, err
	}

	var reply struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("reading secret %s: %w", name, err)
	}
	raw, err := base64.StdEncoding.DecodeString(reply.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("secret %s is not valid base64: %w", name, err)
	}

	data, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("secret %s: %w", name, err)
	}
	return data, nil
}

// Parse decodes a stored secret. Every secret den reads holds a JSON object,
// so that one IAM grant covers everything a given reader needs.
func Parse(raw []byte) (map[string]string, error) {
	var data map[string]string
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	return data, nil
}

// accessToken returns a cached token, minting one when it is close enough to
// expiry that a call made now might outlive it.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	if c.token != "" && time.Now().Before(c.expiry.Add(-time.Minute)) {
		return c.token, nil
	}

	assertion, err := c.assertion()
	if err != nil {
		return "", err
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.key.TokenURI, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("requesting an access token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, err := c.do(req, "requesting an access token")
	if err != nil {
		return "", err
	}

	var reply struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", fmt.Errorf("requesting an access token: %w", err)
	}
	if reply.AccessToken == "" {
		return "", fmt.Errorf("the token endpoint returned no access_token")
	}

	c.token = reply.AccessToken
	c.expiry = time.Now().Add(time.Duration(reply.ExpiresIn) * time.Second)
	return c.token, nil
}

// assertion builds the signed JWT that is exchanged for an access token.
func (c *Client) assertion() (string, error) {
	now := time.Now()
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"kid": c.key.PrivateKeyID,
	}
	claims := map[string]any{
		"iss":   c.key.ClientEmail,
		"scope": scope,
		"aud":   c.key.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}

	segments := make([]string, 0, 3)
	for _, part := range []any{header, claims} {
		encoded, err := json.Marshal(part)
		if err != nil {
			return "", fmt.Errorf("building the token request: %w", err)
		}
		segments = append(segments, base64.RawURLEncoding.EncodeToString(encoded))
	}

	signed := strings.Join(segments, ".")
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.signer, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing the token request: %w", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// do sends req and returns the body, turning a non-2xx into an error that
// carries Google's own explanation. Google does not echo credentials in these
// replies, and without the body the caller sees only a status code.
func (c *Client) do(req *http.Request, what string) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s: %s: %s",
			what, resp.Status, string(bytes.TrimSpace(body)))
	}
	return body, nil
}

// parsePrivateKey reads the PEM-wrapped PKCS#8 RSA key that GCP issues.
func parsePrivateKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, fmt.Errorf("the private_key is not PEM")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Older keys are PKCS#1. Worth the second attempt: the failure mode
		// otherwise is an unusable key with a misleading parse error.
		rsaKey, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if pkcs1Err != nil {
			return nil, fmt.Errorf("the private_key does not parse: %w", err)
		}
		return rsaKey, nil
	}

	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the private_key is %T, want RSA", parsed)
	}
	return rsaKey, nil
}

// JSON renders a secret back to the form it is stored in, so a fetched secret
// and a cached one are byte-identical.
func JSON(data map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return nil, fmt.Errorf("encoding the secret: %w", err)
	}
	return buf.Bytes(), nil
}
