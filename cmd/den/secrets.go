package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dmikalova/den/internal/gcp"
	"github.com/dmikalova/den/internal/notify"
)

// den's runtime secret, and where the pieces of it live on the host.
//
// The service account key is sealed to the TPM, so the copy on disk is
// useless on any other machine. systemd decrypts it through
// LoadCredentialEncrypted= and hands the plaintext to this process alone,
// which is why nothing here ever talks to the TPM itself.
const (
	defaultHostSecret  = "den-host"
	defaultKeyName     = "gcp-key"
	defaultSecretsDir  = "/run/den/secrets"
	defaultSecretCache = "/var/lib/den/den-host.cred"
)

// credentialName is the systemd credential name the cache is sealed under.
// systemd-creds binds the name into the ciphertext, so a credential cannot be
// decrypted by a unit that asks for it under a different name.
const credentialName = "den-host"

func secretsHost(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("secrets needs a subcommand: %w", errUsage)
	}
	switch args[0] {
	case "fetch":
		return secretsFetch(ctx, args[1:], stdout, stderr)
	case "seal":
		return secretsSeal(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown secrets subcommand %q: %w", args[0], errUsage)
	}
}

type secretsFlags struct {
	secret       *string
	dir          *string
	cache        *string
	key          *string
	webhook      *string
	healthchecks *string
}

func addSecretsFlags(fs *flag.FlagSet) secretsFlags {
	return secretsFlags{
		secret: fs.String("secret", defaultHostSecret, "Secret Manager secret to read"),
		dir:    fs.String("dir", defaultSecretsDir, "where to explode the secret"),
		cache:  fs.String("cache", defaultSecretCache, "sealed last-known-good copy"),
		key: fs.String(
			"credential",
			defaultKeyName,
			"systemd credential holding the service account key",
		),
		webhook: fs.String(
			"webhook-file",
			defaultWebhookFile,
			"durable copy of the Discord webhook for the failure notifier",
		),
		healthchecks: fs.String(
			"healthcheck-dir",
			defaultHealthcheckDir,
			"durable copies of the healthcheck URLs, one file per check",
		),
	}
}

// secretsFetch reads den's runtime secret and writes one file per key.
//
// Exploding into files rather than the environment is what lets each unit
// declare the keys it needs with LoadCredential=: systemd copies only those
// into that unit's own namespace, so the unit file is the allowlist and a
// compromised service cannot ask for more than it was written to have.
func secretsFetch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den secrets fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := addSecretsFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	data, stale, err := fetchOrCached(ctx, f, stdout, stderr)
	if err != nil {
		return err
	}
	if err := explode(*f.dir, data); err != nil {
		return err
	}
	if err := writeDurable(*f.webhook, data["DISCORD_WEBHOOK_URL"]); err != nil {
		return err
	}
	if err := writeHealthchecks(*f.healthchecks, data); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "wrote %d secrets to %s\n", len(data), *f.dir); err != nil {
		return err
	}

	// Reported after the explode so the warning can use the webhook it just
	// wrote. A stale secret still works, so this is a warning and not a
	// failure: the unit starting matters more than the value being current.
	if stale {
		warnStale(ctx, data, *f.secret, stderr)
	}
	return nil
}

// fetchOrCached prefers a fresh secret and falls back to the sealed cache,
// reporting whether the value is stale. den must boot without the internet:
// otherwise a cold start during an outage leaves every unit that wants a
// credential unable to start.
func fetchOrCached(
	ctx context.Context,
	f secretsFlags,
	stdout, stderr io.Writer,
) (map[string]string, bool, error) {
	data, err := fetchFresh(ctx, f)
	if err == nil {
		if cacheErr := writeCache(ctx, *f.cache, data); cacheErr != nil {
			// A secret that cannot be cached is still a usable secret, so
			// this only costs the next offline boot.
			_, _ = fmt.Fprintf(stderr, "could not refresh the cache: %v\n", cacheErr)
		}
		return data, false, nil
	}

	_, _ = fmt.Fprintf(stderr, "could not read %s: %v\n", *f.secret, err)
	cached, cacheErr := readCache(ctx, *f.cache)
	if cacheErr != nil {
		return nil, false, fmt.Errorf("%w, and the cache is unusable: %w", err, cacheErr)
	}
	if _, err := fmt.Fprintf(stdout, "falling back to the cached secret\n"); err != nil {
		return nil, false, err
	}
	return cached, true, nil
}

func fetchFresh(ctx context.Context, f secretsFlags) (map[string]string, error) {
	keyJSON, err := readCredential(*f.key)
	if err != nil {
		return nil, err
	}
	client, err := gcp.New(keyJSON)
	if err != nil {
		return nil, err
	}
	return client.Secret(ctx, *f.secret)
}

// readCredential reads a credential systemd has already decrypted for this
// unit. $CREDENTIALS_DIRECTORY is set only for a unit that asked for one, so
// its absence means the unit file is wrong rather than the host is.
func readCredential(name string) ([]byte, error) {
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if dir == "" {
		return nil, fmt.Errorf(
			"CREDENTIALS_DIRECTORY is unset: run this from a unit with LoadCredentialEncrypted=%s",
			name,
		)
	}
	path := filepath.Join(dir, name)
	b, err := os.ReadFile(
		path,
	) // #nosec G304 -- the path is a systemd-supplied directory and a flag.
	if err != nil {
		return nil, fmt.Errorf("reading credential %s: %w", name, err)
	}
	return b, nil
}

// explode writes one file per key, then removes anything left over. Without
// the sweep a key dropped from the secret would survive on the host, which
// turns a revocation into a no-op.
func explode(dir string, data map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("making %s: %w", dir, err)
	}

	wanted := make(map[string]bool, len(data))
	for name, value := range data {
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return fmt.Errorf("secret key %q is not a usable file name", name)
		}
		wanted[name] = true
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value), 0o400); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if wanted[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("removing the stale secret %s: %w", e.Name(), err)
		}
	}
	return nil
}

// writeDurable keeps a copy of one value outside /run, for the units that must
// not depend on this one: a notifier that waits on the secret fetch cannot
// report the secret fetch failing. /etc is inside LUKS, so a stolen disk still
// yields nothing.
//
// An absent key is not an error. den runs before every value exists.
func writeDurable(path, value string) error {
	if value == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("making %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o400); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// healthcheckPrefix marks the keys of den's runtime secret that are ping URLs.
// The rest of the key is the check name, so adding a check to kestrel is all
// it takes for den to start writing its URL.
const healthcheckPrefix = "HEALTHCHECK_URL_"

// writeHealthchecks keeps one durable ping URL per check, named after the job
// that pings it. The sweep matters more here than in explode: a file left
// behind is a check nobody declares any more, and den would go on feeding a
// switch kestrel has already forgotten.
func writeHealthchecks(dir string, data map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("making %s: %w", dir, err)
	}

	wanted := make(map[string]bool)

	for key, value := range data {
		name, ok := strings.CutPrefix(key, healthcheckPrefix)
		if !ok || value == "" {
			continue
		}

		name = strings.ToLower(strings.ReplaceAll(name, "_", "-"))
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return fmt.Errorf("check name %q is not a usable file name", name)
		}

		wanted[name] = true
		path := filepath.Join(dir, name)

		if err := os.WriteFile(path, []byte(value+"\n"), 0o400); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, e := range entries {
		if wanted[e.Name()] {
			continue
		}

		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("removing the stale healthcheck %s: %w", e.Name(), err)
		}
	}

	return nil
}

// secretsSeal encrypts the service account key on stdin against this host's
// TPM. Run on den during bootstrap, because a sealed credential can only be
// produced by the machine that will open it.
func secretsSeal(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("den secrets seal", flag.ContinueOnError)
	fs.SetOutput(stdout)
	name := fs.String("name", defaultKeyName, "credential name to seal under")
	out := fs.String(
		"out",
		"/etc/den/credentials/gcp-key.cred",
		"where to write the sealed credential",
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	plaintext, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return fmt.Errorf("reading the key from stdin: %w", err)
	}
	if len(bytes.TrimSpace(plaintext)) == 0 {
		return fmt.Errorf("no key on stdin")
	}
	if _, err := gcp.New(plaintext); err != nil {
		// Checked before sealing, because an unusable key sealed to the TPM
		// fails much later and much less clearly.
		return err
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return fmt.Errorf("making %s: %w", filepath.Dir(*out), err)
	}
	if err := seal(ctx, *name, *out, plaintext); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "sealed %s to the TPM at %s\n", *name, *out)
	return err
}

// seal and unseal shell out to systemd-creds, which already knows how to talk
// to the TPM and is present wherever systemd is.
//
// --tpm2-pcrs= is empty on purpose. The default binds to PCR 7, which makes
// the credential undecryptable after a firmware or Secure Boot change. The
// threat here is a stolen disk, not a local attacker who can rewrite the boot
// chain, so presence of this TPM is the right condition.
func seal(ctx context.Context, name, out string, plaintext []byte) error {
	cmd := exec.CommandContext(ctx, "systemd-creds", "encrypt",
		"--name="+name, "--with-key=tpm2", "--tpm2-pcrs=", "-", out)
	cmd.Stdin = bytes.NewReader(plaintext)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sealing %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	// systemd-creds writes 0644. The ciphertext is useless off this TPM, but
	// it is still a credential and the directories are the only thing keeping
	// it unreadable today.
	if err := os.Chmod(out, 0o600); err != nil {
		return fmt.Errorf("securing %s: %w", out, err)
	}
	return nil
}

func unseal(ctx context.Context, name, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "systemd-creds", "decrypt", "--name="+name, path, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf(
			"unsealing %s: %w: %s",
			name,
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	return stdout.Bytes(), nil
}

func writeCache(ctx context.Context, path string, data map[string]string) error {
	raw, err := gcp.JSON(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("making %s: %w", filepath.Dir(path), err)
	}
	// systemd-creds will not overwrite, and a half-written cache is worse
	// than none: it would fail the next offline boot instead of the fetch.
	tmp := path + ".new"
	_ = os.Remove(tmp)
	if err := seal(ctx, credentialName, tmp, raw); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

func readCache(ctx context.Context, path string) (map[string]string, error) {
	raw, err := unseal(ctx, credentialName, path)
	if err != nil {
		return nil, err
	}
	data, err := gcp.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("the cached secret is unreadable: %w", err)
	}
	return data, nil
}

// warnStale reports that den is running on a cached secret. Silence here
// would turn a temporary outage into a secret that is quietly months old.
func warnStale(ctx context.Context, data map[string]string, secret string, stderr io.Writer) {
	webhook := data["DISCORD_WEBHOOK_URL"]
	if webhook == "" {
		return
	}

	host, err := os.Hostname()
	if err != nil {
		host = "den"
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err = notify.Send(ctx, webhook, notify.Message{
		Title: "den is using a cached secret",
		Body: fmt.Sprintf(
			"%s could not be read from Secret Manager, so den fell back to the "+
				"sealed copy on disk. Services are running, but the values may be stale.",
			secret),
		Level: notify.LevelWarn,
		Host:  host,
		Fields: []notify.Field{
			{Name: "Keys", Value: strings.Join(keys, ", ")},
		},
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "could not report the stale secret: %v\n", err)
	}
}
