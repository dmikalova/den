// Package secrets reads a SOPS-encrypted JSON file, and reads GCP Secret
// Manager through gcloud. den shells out in both cases so neither the age key
// nor a GCP credential passes through this process.
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
	raw, err := Access(ctx, project, name)
	if err != nil {
		return "", err
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return "", fmt.Errorf("secret %s is not a JSON object: %w", name, err)
	}
	value, ok := data[key]
	if !ok {
		return "", fmt.Errorf("secret %s has no %s", name, key)
	}
	return value, nil
}

// Load decrypts path and returns the install secrets it holds.
func Load(ctx context.Context, path string) (Install, error) {
	if _, err := exec.LookPath("sops"); err != nil {
		return Install{}, fmt.Errorf("sops is not installed: brew install sops")
	}

	cmd := exec.CommandContext(ctx, "sops", "-d", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return Install{}, fmt.Errorf("sops -d %s: %w: %s",
			path, err, strings.TrimSpace(stderr.String()))
	}

	var s Install
	if err := json.Unmarshal(out, &s); err != nil {
		return Install{}, fmt.Errorf("%s does not decrypt to JSON: %w", path, err)
	}
	if s.Password == "" {
		return Install{}, fmt.Errorf("%s has no PASSWORD", path)
	}
	if s.LUKSPassphrase == "" {
		return Install{}, fmt.Errorf("%s has no LUKS_PASSPHRASE", path)
	}
	return s, nil
}
