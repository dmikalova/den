// Package secrets reads a SOPS-encrypted JSON file. den shells out to the
// sops binary so the age key never passes through this process.
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
