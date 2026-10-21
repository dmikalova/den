// Package secrets reads GCP Secret Manager through gcloud, so no GCP
// credential passes through this process.
package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Install holds what `den usb build` must not keep in the repository in clear.
// The console password is plaintext here because den derives the crypt(3) hash
// at build time.
type Install struct {
	Password       string `json:"PASSWORD"`
	LUKSPassphrase string `json:"LUKS_PASSPHRASE"`
	HostKey        string `json:"SSH_HOST_ED25519_KEY"`
}

// Access returns the newest version of a GCP Secret Manager secret, using
// whatever credentials gcloud already holds.
func Access(ctx context.Context, project, name string) (string, error) {
	if _, err := exec.LookPath("gcloud"); err != nil {
		return "", fmt.Errorf("gcloud is not installed: brew install --cask gcloud-cli")
	}

	cmd := exec.CommandContext(ctx, "gcloud", "secrets", "versions", "access", "latest",
		"--secret="+name, "--project="+project)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// gcloud says why in stderr, so losing it leaves only "exit status 1".
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("reading secret %s: %w\n%s", name, err, msg)
		}
		return "", fmt.Errorf("reading secret %s: %w", name, err)
	}
	return string(bytes.TrimSpace(out)), nil
}

// AccessKey returns one key out of a JSON secret. Secret Manager secrets hold
// a JSON object rather than a bare value, so that one grant covers everything
// a given reader needs; the key names match the SOPS file they came from.
func AccessKey(ctx context.Context, project, name, key string) (string, error) {
	data, err := AccessJSON(ctx, project, name)
	if err != nil {
		return "", err
	}
	value, ok := data[key]
	if !ok {
		return "", fmt.Errorf("secret %s has no %s", name, key)
	}
	return value, nil
}

// AccessJSON returns every key of a JSON secret.
func AccessJSON(ctx context.Context, project, name string) (map[string]string, error) {
	raw, err := Access(ctx, project, name)
	if err != nil {
		return nil, err
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("secret %s is not a JSON object: %w", name, err)
	}
	return data, nil
}
