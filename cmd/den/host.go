package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/dmikalova/den/internal/bootstrap"
	"github.com/dmikalova/den/internal/remote"
	"github.com/dmikalova/den/internal/secrets"
)

// defaultHost is the ssh destination. Put a Host block in ~/.ssh/config so a
// reinstall only changes one file.
const defaultHost = "den"

// The tailnet enrolment key lives in GCP Secret Manager, written there by the
// infrastructure repo's tailscale stack.
const (
	defaultProject      = "mklv-infrastructure"
	defaultTailscaleKey = "tailscale-den-authkey"
)

// hostFlags are the flags den bootstrap and den apply share.
type hostFlags struct {
	host    *string
	user    *string
	repo    *string
	dir     *string
	mgmt    *string
	project *string
	tsKey   *string
	tsTag   *string
	dryRun  *bool
	noop    *bool
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
		project: fs.String(
			"gcp-project",
			defaultProject,
			"GCP project holding the tailnet key",
		),
		tsKey: fs.String(
			"tailscale-secret",
			defaultTailscaleKey,
			"Secret Manager secret holding the tailnet key",
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

// tailnetKey fetches den's enrolment key. A missing or unreadable secret is
// not fatal: converging matters more than enrolling, and the join step guards
// itself anyway. Dry runs never reach gcloud, because the script they print
// would contain the key.
//
// gcloud can stall here while it refreshes credentials, so this announces
// itself first rather than leaving the run looking hung.
func (f hostFlags) tailnetKey(ctx context.Context, dryRun bool, stdout, stderr io.Writer) string {
	if dryRun {
		return "REDACTED"
	}
	if _, err := fmt.Fprintf(stdout, "reading %s from %s\n", *f.tsKey, *f.project); err != nil {
		return ""
	}
	// Bounded because the join is optional and gcloud can block for a long
	// time on a credential or network problem. Converging still matters.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	key, err := secrets.Access(ctx, *f.project, *f.tsKey)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("gcloud did not answer within 30s: %w", err)
		}
		_, _ = fmt.Fprintf(stderr, "skipping the tailnet join: %v\n", err)
		return ""
	}
	if _, err := fmt.Fprintf(
		stdout,
		"done in %s\n",
		time.Since(start).Round(time.Second),
	); err != nil {
		return ""
	}
	return key
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

func bootstrapHost(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den bootstrap", flag.ContinueOnError)
	f := addHostFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	h, o := f.split(stdout, stderr)

	if _, err := fmt.Fprintf(stdout, "waiting for %s\n", h.Addr); err != nil {
		return err
	}
	if err := h.Wait(ctx); err != nil {
		return err
	}
	if err := bootstrap.Run(ctx, h, bootstrap.Bootstrap(o), stdout); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "\nbootstrapped. Next:\n  den apply --host %s\n", h.Addr)
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
	o.TailscaleKey = f.tailnetKey(ctx, h.DryRun, stdout, stderr)

	if err := bootstrap.Run(ctx, h, bootstrap.Apply(o), stdout); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "\napplied")
	return err
}
