package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/dmikalova/den/v3/internal/check"
	"github.com/dmikalova/den/v3/internal/remote"
)

// checkHost runs the smoke checks and reports them. It never changes the host,
// so it is safe to run at any time, including against a converge in progress.
func checkHost(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", defaultHost, "ssh destination, an alias or user@address")
	user := fs.String("user", "dmikalova", "the login account on den")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("den check: %w", errUsage)
	}

	h := remote.Host{Addr: *host, Stdout: stdout, Stderr: stderr}
	return runChecks(ctx, h, *user, stdout)
}

// runChecks runs the smoke checks on h and prints one line for each.
func runChecks(ctx context.Context, h remote.Host, user string, stdout io.Writer) error {
	out, err := h.Capture(ctx, check.Script(user))
	if err != nil {
		return fmt.Errorf("running checks on %s: %w", h.Addr, err)
	}

	results := check.Parse(out)
	if len(results) == 0 {
		return fmt.Errorf("%s reported no checks at all", h.Addr)
	}
	for _, r := range results {
		mark := "ok  "
		if !r.OK {
			mark = "FAIL"
		}
		_, _ = fmt.Fprintf(stdout, "%s %s\n", mark, r.Name)
	}

	if failed := check.Failed(results); len(failed) > 0 {
		return fmt.Errorf("%d of %d checks failed", len(failed), len(results))
	}
	_, _ = fmt.Fprintf(stdout, "\nall %d checks passed\n", len(results))
	return nil
}
