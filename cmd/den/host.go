package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/dmikalova/den/internal/bootstrap"
	"github.com/dmikalova/den/internal/remote"
)

// defaultHost is the ssh destination. Put a Host block in ~/.ssh/config so a
// reinstall only changes one file.
const defaultHost = "den"

// hostFlags are the flags den bootstrap and den apply share.
type hostFlags struct {
	host    *string
	user    *string
	repo    *string
	dir     *string
	chezmoi *string
	dryRun  *bool
}

func addHostFlags(fs *flag.FlagSet) hostFlags {
	return hostFlags{
		host: fs.String("host", defaultHost, "ssh destination, an alias or user@address"),
		user: fs.String("user", "dmikalova", "the login account on den"),
		repo: fs.String(
			"repo",
			bootstrap.DefaultConfigRepo,
			"git remote holding the chezmoi trees",
		),
		dir: fs.String("config-dir", bootstrap.DefaultConfigDir, "where that remote is cloned"),
		chezmoi: fs.String(
			"chezmoi-version",
			bootstrap.DefaultChezmoiVersion,
			"chezmoi release to install",
		),
		dryRun: fs.Bool("dry-run", false, "print every script instead of running it"),
	}
}

func (f hostFlags) split(stdout, stderr io.Writer) (remote.Host, bootstrap.Options) {
	return remote.Host{
		Addr:   *f.host,
		Stdout: stdout,
		Stderr: stderr,
		DryRun: *f.dryRun,
	}, bootstrap.Options{
		User:           *f.user,
		ChezmoiVersion: *f.chezmoi,
		ConfigRepo:     *f.repo,
		ConfigDir:      *f.dir,
	}
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

	if err := bootstrap.Run(ctx, h, bootstrap.Apply(o), stdout); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "\napplied")
	return err
}
