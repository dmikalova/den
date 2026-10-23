// Package tailnet talks to the Tailscale API with den's OAuth client. It does
// the steps den cannot do for itself on a reinstall: removing the node the old
// install left behind, so the new one enrols as den rather than den-1, and
// setting den's fixed IPv4, so every record that points at den stays correct.
package tailnet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// DefaultBaseURL is the Tailscale control plane API.
const DefaultBaseURL = "https://api.tailscale.com"

const timeout = 30 * time.Second

// Client holds an access token minted from the OAuth client credentials.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
}

// Device is the part of a tailnet device this package reads.
type Device struct {
	NodeID             string   `json:"nodeId"`
	Hostname           string   `json:"hostname"`
	Name               string   `json:"name"`
	Tags               []string `json:"tags"`
	ConnectedToControl bool     `json:"connectedToControl"`
}

// New exchanges the client credentials for an access token. The secret is a
// bearer credential, so it never appears in a returned error.
func New(ctx context.Context, baseURL, clientID, clientSecret string) (*Client, error) {
	if clientID == "" || clientSecret == "" {
		return nil, errors.New("no Tailscale OAuth client configured")
	}
	c := &Client{baseURL: strings.TrimSuffix(baseURL, "/"), http: &http.Client{Timeout: timeout}}

	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/v2/oauth/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("building the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.do(req, &tok); err != nil {
		return nil, fmt.Errorf("exchanging the OAuth client for a token: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("the token response carried no access token")
	}
	c.token = tok.AccessToken
	return c, nil
}

// Devices lists every device the client can see.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	// all, because connectedToControl is what tells a dead node from a live one.
	req, err := c.request(ctx, http.MethodGet, "/api/v2/tailnet/-/devices?fields=all")
	if err != nil {
		return nil, err
	}
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do(req, &out); err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	return out.Devices, nil
}

// Delete removes a device from the tailnet.
func (c *Client) Delete(ctx context.Context, nodeID string) error {
	req, err := c.request(ctx, http.MethodDelete, "/api/v2/device/"+url.PathEscape(nodeID))
	if err != nil {
		return err
	}
	if err := c.do(req, nil); err != nil {
		return fmt.Errorf("deleting device %s: %w", nodeID, err)
	}
	return nil
}

// SetIPv4 gives a device a specific tailnet IPv4 address. The address must be
// free, so the device that held it has to be deleted first. Tailscale has no
// equivalent for IPv6.
func (c *Client) SetIPv4(ctx context.Context, nodeID, ipv4 string) error {
	body, err := json.Marshal(map[string]string{"ipv4": ipv4})
	if err != nil {
		return fmt.Errorf("encoding the address: %w", err)
	}
	req, err := c.request(ctx, http.MethodPost, "/api/v2/device/"+url.PathEscape(nodeID)+"/ip")
	if err != nil {
		return err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	if err := c.do(req, nil); err != nil {
		return fmt.Errorf("setting %s's IPv4 to %s: %w", nodeID, ipv4, err)
	}
	return nil
}

// Stale picks the devices a fresh enrolment as hostname would collide with:
// the same hostname, carrying tag, and not connected. A connected device is
// never stale, whatever its name.
func Stale(devices []Device, hostname, tag string) []Device {
	var out []Device
	for _, d := range devices {
		if d.Hostname == hostname && slices.Contains(d.Tags, tag) && !d.ConnectedToControl {
			out = append(out, d)
		}
	}
	return out
}

func (c *Client) request(ctx context.Context, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("building %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return req, nil
}

func (c *Client) do(req *http.Request, into any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, string(bytes.TrimSpace(body)))
	}
	if into == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("decoding the response: %w", err)
	}
	return nil
}
