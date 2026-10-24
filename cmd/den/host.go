package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/dmikalova/den/v3/internal/bootstrap"
	"github.com/dmikalova/den/v3/internal/remote"
	"github.com/dmikalova/den/v3/internal/secrets"
	"github.com/dmikalova/den/v3/internal/tailnet"
)

// defaultHost is the ssh destination. den answers to den.local over mDNS on
// the LAN, so no machine needs a Host block. Off the LAN, pass --host den to
// reach it through MagicDNS.
const defaultHost = "dmikalova@den.local"

// den's tailnet OAuth client lives in GCP Secret Manager, written there by the
// infrastructure repo's tailscale stack. The secret is a JSON object, so the
// keys inside it have to be named too.
const (
	defaultProject        = "mklv-infrastructure"
	defaultTailscaleKey   = "tailscale-den"
	tailscaleClientID     = "CLIENT_ID"
	tailscaleClientSecret = "CLIENT_SECRET"
)

// rebootTimeout bounds the wait for den to come back. Past it, den is most
// likely sitting at a LUKS prompt because the TPM refused to unseal.
const rebootTimeout = 15 * time.Minute

// den-install holds what builds the machine. It carries no IAM binding at
// all, so only project admins read it, which is what keeps den out of it.
const (
	defaultInstallSecret = "den-install"
	gcpKeyName           = "GCP_KEY"
	sealedKeyPath        = "/etc/den/credentials/gcp-key.cred"
)

// hostFlags are the flags den bootstrap and den apply share.
type hostFlags struct {
	host     *string
	user     *string
	repo     *string
	dir      *string
	mgmt     *string
	project  *string
	install  *string
	tsKey    *string
	tsTag    *string
	dryRun   *bool
	noop     *bool
	noReboot *bool
}

func addHostFlags(fs *flag.FlagSet) hostFlags {
	return hostFlags{
		host: fs.String("host", defaultHost, "ssh destination, an alias or user@address"),
		user: fs.String("user", "dmikalova", "the login account on den"),
		repo: fs.String(
			"repo",
			bootstrap.DefaultConfigRepo,
			"git remote holding the mcl source",
		),
		dir: fs.String("config-dir", bootstrap.DefaultConfigDir, "where that remote is cloned"),
		mgmt: fs.String(
			"mgmt-version",
			bootstrap.DefaultMgmtVersion,
			"mgmt release to install",
		),
		dryRun: fs.Bool("dry-run", false, "print every script instead of running it"),
		noop:   fs.Bool("noop", false, "run mgmt but report changes instead of making them"),
		noReboot: fs.Bool(
			"no-reboot",
			false,
			"do not reboot den when a package or the GPU driver needs it",
		),
		project: fs.String(
			"gcp-project",
			defaultProject,
			"GCP project holding the tailnet key",
		),
		install: fs.String(
			"install-secret",
			defaultInstallSecret,
			"Secret Manager secret holding den's service account key",
		),
		tsKey: fs.String(
			"tailscale-secret",
			defaultTailscaleKey,
			"Secret Manager secret holding den's tailnet OAuth client",
		),
		tsTag: fs.String(
			"tailscale-tag",
			bootstrap.DefaultTailscaleTag,
			"policy tag den advertises when it enrols",
		),
	}
}

func (f hostFlags) split(stdout, stderr io.Writer) (remote.Host, bootstrap.Options) {
	return remote.Host{
		Addr:   *f.host,
		Stdout: stdout,
		Stderr: stderr,
		DryRun: *f.dryRun,
	}, bootstrap.Options{
		User:         *f.user,
		MgmtVersion:  *f.mgmt,
		ConfigRepo:   *f.repo,
		ConfigDir:    *f.dir,
		Terminfo:     localTerminfo(),
		TailscaleTag: *f.tsTag,
		Noop:         *f.noop,
	}
}

// tailnetAuth is den's tailnet OAuth client. The secret enrols den, and the
// pair together let den apply clear the node a reinstall left behind.
type tailnetAuth struct {
	id, secret string
}

// authKey is the client secret in the form tailscale up accepts. An OAuth
// secret mints a key per use, and these attributes say which kind.
func (a tailnetAuth) authKey() string {
	if a.secret == "" {
		return ""
	}
	return a.secret + "?ephemeral=false&preauthorized=true"
}

// tailnetAuth fetches den's OAuth client. A missing or unreadable secret is
// not fatal: converging matters more than enrolling, and the join step guards
// itself anyway. Dry runs never reach gcloud, because the script they print
// would contain the secret.
//
// gcloud can stall here while it refreshes credentials, so this announces
// itself first rather than leaving the run looking hung.
func (f hostFlags) tailnetAuth(
	ctx context.Context,
	dryRun bool,
	stdout, stderr io.Writer,
) tailnetAuth {
	if dryRun {
		return tailnetAuth{id: "REDACTED", secret: "REDACTED"}
	}
	if _, err := fmt.Fprintf(stdout, "reading %s from %s\n", *f.tsKey, *f.project); err != nil {
		return tailnetAuth{}
	}
	// Bounded because the join is optional and gcloud can block for a long
	// time on a credential or network problem. Converging still matters.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	data, err := secrets.AccessJSON(ctx, *f.project, *f.tsKey)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("gcloud did not answer within 30s: %w", err)
		}
		_, _ = fmt.Fprintf(stderr, "skipping the tailnet join: %v\n", err)
		return tailnetAuth{}
	}
	auth := tailnetAuth{id: data[tailscaleClientID], secret: data[tailscaleClientSecret]}
	if auth.id == "" || auth.secret == "" {
		_, _ = fmt.Fprintf(
			stderr,
			"skipping the tailnet join: %s lacks %s or %s\n",
			*f.tsKey,
			tailscaleClientID,
			tailscaleClientSecret,
		)
		return tailnetAuth{}
	}
	if _, err := fmt.Fprintf(
		stdout,
		"done in %s\n",
		time.Since(start).Round(time.Second),
	); err != nil {
		return tailnetAuth{}
	}
	return auth
}

// tailnetIPv4 is den's fixed tailnet address. The den/dns stack in the
// infrastructure repo publishes the same address, so keep the two in step.
const tailnetIPv4 = "100.64.0.1"

// enrol joins den to the tailnet unless it is already on it, then makes sure
// den holds tailnetIPv4. A tailscaled with no node identity means a fresh
// install, and the previous install's node is still registered under den, so
// it is removed first or this one enrols as den-1 and keeps the address.
func (f hostFlags) enrol(
	ctx context.Context,
	h remote.Host,
	o bootstrap.Options,
	auth tailnetAuth,
	stdout, stderr io.Writer,
) error {
	if auth.secret == "" || o.Noop {
		return nil
	}
	join := []bootstrap.Step{bootstrap.JoinTailnet(o)}
	if h.DryRun {
		return bootstrap.Run(ctx, h, join, stdout)
	}

	state, err := readTailnetState(ctx, h)
	if err != nil {
		return err
	}
	if state.BackendState == "" {
		_, err := fmt.Fprintln(stdout, "\ntailscale is not installed yet, skipping the join")
		return err
	}

	client, err := tailnet.New(ctx, tailnet.DefaultBaseURL, auth.id, auth.secret)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "could not reach the Tailscale API: %v\n", err)
	}

	if state.BackendState == "Running" {
		if _, err := fmt.Fprintln(stdout, "\nalready on the tailnet"); err != nil {
			return err
		}
	} else {
		if client != nil &&
			(state.BackendState == "NeedsLogin" || state.BackendState == "NoState") {
			f.removeStaleNode(ctx, h, client, stdout, stderr)
		}
		if err := bootstrap.Run(ctx, h, join, stdout); err != nil {
			return err
		}
	}
	if client != nil {
		pinIPv4(ctx, h, client, stdout, stderr)
	}
	return nil
}

// tailnetState is the part of `tailscale status --json` den reads.
type tailnetState struct {
	BackendState string
	Self         struct {
		ID           string
		TailscaleIPs []string
	}
}

func readTailnetState(ctx context.Context, h remote.Host) (tailnetState, error) {
	var state tailnetState
	raw, err := h.Capture(ctx, bootstrap.TailnetState)
	if err != nil {
		return state, fmt.Errorf("reading tailscaled's state: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return state, fmt.Errorf("reading tailscaled's state: %w", err)
	}
	return state, nil
}

// pinIPv4 gives den tailnetIPv4 if it does not already hold it. A failure only
// warns: den works on any address, but *.den.mklv.tech does not reach it.
func pinIPv4(
	ctx context.Context,
	h remote.Host,
	client *tailnet.Client,
	stdout, stderr io.Writer,
) {
	warn := func(err error) {
		_, _ = fmt.Fprintf(
			stderr,
			"could not give den its tailnet address %s, set it in the Tailscale admin console: %v\n",
			tailnetIPv4,
			err,
		)
	}
	state, err := readTailnetState(ctx, h)
	if err != nil {
		warn(err)
		return
	}
	if slices.Contains(state.Self.TailscaleIPs, tailnetIPv4) {
		return
	}
	if state.Self.ID == "" {
		warn(errors.New("tailscaled reported no node ID"))
		return
	}
	_, _ = fmt.Fprintf(stdout, "setting den's tailnet address to %s\n", tailnetIPv4)
	if err := client.SetIPv4(ctx, state.Self.ID, tailnetIPv4); err != nil {
		warn(err)
	}
}

// removeStaleNode deletes the disconnected namesakes of this host. A failure
// only warns: the join still works, it just lands as den-1.
func (f hostFlags) removeStaleNode(
	ctx context.Context,
	h remote.Host,
	client *tailnet.Client,
	stdout, stderr io.Writer,
) {
	warn := func(err error) {
		_, _ = fmt.Fprintf(
			stderr,
			"could not clear the old tailnet node, den may enrol as den-1: %v\n",
			err,
		)
	}
	hostname, err := h.Capture(ctx, "hostname")
	if err != nil {
		warn(err)
		return
	}
	devices, err := client.Devices(ctx)
	if err != nil {
		warn(err)
		return
	}
	for _, d := range tailnet.Stale(devices, hostname, *f.tsTag) {
		_, _ = fmt.Fprintf(stdout, "removing the stale tailnet node %s (%s)\n", d.Name, d.NodeID)
		if err := client.Delete(ctx, d.NodeID); err != nil {
			warn(err)
		}
	}
}

// rebootIfNeeded reboots den and waits for it to come back on a new boot. It
// lives here rather than in mgmt because mgmt also runs from the nightly
// reconcile timer, and a reboot nobody is watching is the one the decision
// log rejects. den apply always has an operator.
func (f hostFlags) rebootIfNeeded(
	ctx context.Context,
	h remote.Host,
	o bootstrap.Options,
	stdout io.Writer,
) error {
	if h.DryRun || o.Noop || *f.noReboot {
		return nil
	}
	reason, err := h.Capture(ctx, bootstrap.RebootReason)
	if err != nil {
		return fmt.Errorf("checking whether den needs a reboot: %w", err)
	}
	if reason == "" {
		return nil
	}
	if _, err := fmt.Fprintf(stdout, "\nrebooting den: %s\n", reason); err != nil {
		return err
	}
	if err := rebootAndWait(ctx, h, stdout); err != nil {
		return err
	}
	return h.Run(ctx, bootstrap.MarkRebooted)
}

// rebootAndWait reboots den and waits until it is up on a new boot. It hands
// a LUKS prompt to this terminal, as waitUp does.
func rebootAndWait(ctx context.Context, h remote.Host, stdout io.Writer) error {
	before, err := h.Capture(ctx, bootstrap.BootID)
	if err != nil {
		return fmt.Errorf("reading the boot id: %w", err)
	}
	// The connection drops mid-command, so its error says nothing.
	_ = h.Run(ctx, "sudo systemctl reboot\n")

	wait, cancel := context.WithTimeout(ctx, rebootTimeout)
	defer cancel()
	start := time.Now()
	for {
		if err := waitUp(wait, h, stdout); err != nil {
			return fmt.Errorf("den did not come back from the reboot: %w", err)
		}
		if now, err := h.Capture(wait, bootstrap.BootID); err == nil && now != "" && now != before {
			_, err := fmt.Fprintf(stdout, "back in %s\n", time.Since(start).Round(time.Second))
			return err
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("den did not come back from the reboot: %w", wait.Err())
		case <-time.After(5 * time.Second):
		}
	}
}

// waitUp blocks until den answers ssh as the login user. If den stops in the
// initramfs at the LUKS prompt instead, it hands this terminal to the
// password agent there, so the operator types the passphrase in place. That
// happens on every first boot, because the TPM is enrolled during it, and on
// any boot where the TPM refuses to unseal.
func waitUp(ctx context.Context, h remote.Host, stdout io.Writer) error {
	if h.DryRun {
		return nil
	}
	root := h.As("root")
	for {
		if h.Up(ctx) {
			return nil
		}
		if root.Probe(ctx, "test -e /etc/initrd-release") {
			_, _ = fmt.Fprintf(stdout, "\nden is waiting for its LUKS passphrase\n")
			// The agent exits once it answers, and ssh drops as den leaves the
			// initramfs, so its error says nothing. The next probe decides.
			_ = root.Interactive(ctx, "systemd-tty-ask-password-agent")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", h.Addr, ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
}

// mode names a run that is not a real one, so a report-only run cannot be
// mistaken for a converge that did nothing.
func mode(dryRun, noop bool) string {
	switch {
	case dryRun:
		return " (dry run, nothing is contacted)"
	case noop:
		return " (noop, changes are reported but not made)"
	}
	return ""
}

// localTerminfo describes the terminal den is being driven from, so the server
// can be taught about it. A terminal the local system cannot describe is not an
// error: the step is simply skipped.
func localTerminfo() string {
	term := os.Getenv("TERM")
	if term == "" {
		return ""
	}
	out, err := exec.Command("infocmp", "-x", term).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// sealKey gives den the one credential it ever holds. The key is minted and
// stored by the infrastructure repo, and den-install carries no binding, so a
// laptop with project admin is the only thing that can read it.
//
// The key travels inside the script on ssh stdin: never an argument, never
// staged to a file, never written to local disk. Re-sealing on every apply is
// how a rotation reaches den, and is a no-op otherwise.
//
// A failure here is reported rather than fatal. den-secrets will say so
// loudly on its own, and a half-converged host is worse than an unsealed one.
func (f hostFlags) sealKey(ctx context.Context, h remote.Host, stdout, stderr io.Writer) {
	if h.DryRun {
		return
	}
	if _, err := fmt.Fprintf(stdout, "sealing %s from %s\n", gcpKeyName, *f.install); err != nil {
		return
	}
	read, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	key, err := secrets.AccessKey(read, *f.project, *f.install, gcpKeyName)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "skipping the key seal: %v\n", err)
		return
	}

	// A quoted delimiter stops the shell touching the body, and the key is
	// JSON, so it cannot contain a bare line matching the delimiter.
	script := fmt.Sprintf(`set -euo pipefail
sudo install -d -m 0700 %s
sudo /usr/local/bin/den secrets seal --name=%s --out=%s <<'DEN_GCP_KEY_EOF'
%s
DEN_GCP_KEY_EOF
sudo systemctl restart den-secrets.service
# A fresh host enables caddy but cannot start it before this seal. Start it
# here rather than wait for a reboot: bootstrap reboots only when the host
# needs it. start is a no-op on a caddy that already runs.
if systemctl is-enabled -q caddy.service 2>/dev/null; then
  sudo systemctl start caddy.service
fi
`, filepath.Dir(sealedKeyPath), defaultKeyName, sealedKeyPath, key)

	if err := h.Run(ctx, script); err != nil {
		_, _ = fmt.Fprintf(stderr, "sealing the key failed: %v\n", err)
	}
}

func bootstrapHost(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den bootstrap", flag.ContinueOnError)
	f := addHostFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	h, o := f.split(stdout, stderr)

	if _, err := fmt.Fprintf(
		stdout,
		"bootstrapping %s%s\nwaiting for it to answer\n",
		h.Addr,
		mode(h.DryRun, o.Noop),
	); err != nil {
		return err
	}
	if err := waitUp(ctx, h, stdout); err != nil {
		return err
	}
	if err := bootstrap.Run(ctx, h, bootstrap.Bootstrap(o), stdout); err != nil {
		return err
	}
	if err := f.apply(ctx, h, o, stdout, stderr); err != nil {
		return err
	}
	// A noop converges nothing, so there is nothing yet to prove.
	if o.Noop {
		return nil
	}
	if err := bootstrap.Run(ctx, h, bootstrap.Prove(), stdout); err != nil {
		return err
	}
	if !h.DryRun {
		if _, err := fmt.Fprintf(stdout, "\nchecking\n"); err != nil {
			return err
		}
		if err := runChecks(ctx, h, o.User, stdout); err != nil {
			return fmt.Errorf(
				"%w: the installer stick is kept, so a reinstall needs no rebuild",
				err,
			)
		}
	}
	if err := bootstrap.Run(ctx, h, []bootstrap.Step{bootstrap.EraseStick()}, stdout); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "\nden is installed\n")
	return err
}

func applyHost(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den apply", flag.ContinueOnError)
	f := addHostFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	h, o := f.split(stdout, stderr)
	if _, err := fmt.Fprintf(
		stdout,
		"applying to %s%s\n",
		h.Addr,
		mode(h.DryRun, o.Noop),
	); err != nil {
		return err
	}
	if err := f.apply(ctx, h, o, stdout, stderr); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "\napplied. Next:\n  den check --host %s\n", h.Addr)
	return err
}

// apply is the converge that den apply runs alone and den bootstrap runs
// after its first pass.
func (f hostFlags) apply(
	ctx context.Context,
	h remote.Host,
	o bootstrap.Options,
	stdout, stderr io.Writer,
) error {
	auth := f.tailnetAuth(ctx, h.DryRun, stdout, stderr)
	o.TailscaleKey = auth.authKey()

	if err := bootstrap.Run(ctx, h, bootstrap.ApplyRoot(o), stdout); err != nil {
		return err
	}
	if err := f.enrol(ctx, h, o, auth, stdout, stderr); err != nil {
		return err
	}
	// After the converge, because this needs the den binary mgmt installs.
	f.sealKey(ctx, h, stdout, stderr)
	if err := f.rebootIfNeeded(ctx, h, o, stdout); err != nil {
		return err
	}
	return bootstrap.Run(ctx, h, bootstrap.ApplyUser(o), stdout)
}
