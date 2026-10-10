// Package remote runs commands on den over ssh. It shells out to the ssh
// binary so the caller's ssh config, agent and known_hosts all apply. den
// never implements host key verification of its own.
package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// stepDir stages a root step before sudo runs it. It sits under the login
// user's home at mode 0700 because /tmp is writable by every local user.
const stepDir = ".cache/den"

// probeTimeout bounds one connection attempt while waiting for a reboot.
const probeTimeout = "5"

// Host is an ssh destination: a Host alias from ssh_config, or user@address.
type Host struct {
	Addr   string
	Stdout io.Writer
	Stderr io.Writer

	// DryRun prints each script instead of running it.
	DryRun bool
}

// Run feeds script to bash on the host as the login user. The script travels
// on stdin, so no part of it is ever parsed as an ssh argument.
func (h Host) Run(ctx context.Context, script string) error {
	if h.DryRun {
		return h.print("run", script)
	}
	cmd := h.command(ctx, false, "bash -s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = h.Stdout
	cmd.Stderr = h.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", h.Addr, err)
	}
	return nil
}

// RunRoot stages script in the login user's home and runs it under sudo with
// a terminal attached, so sudo can prompt for a password.
func (h Host) RunRoot(ctx context.Context, script string) error {
	if h.DryRun {
		return h.print("run as root", script)
	}
	stage := fmt.Sprintf("umask 077 && mkdir -p %s && cat > %s/step.sh", stepDir, stepDir)
	cmd := h.command(ctx, false, stage)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stderr = h.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("staging step on %s: %w", h.Addr, err)
	}

	run := fmt.Sprintf("sudo bash %s/step.sh; rc=$?; rm -f %s/step.sh; exit $rc", stepDir, stepDir)
	tty := h.command(ctx, true, run)
	tty.Stdin = os.Stdin
	tty.Stdout = h.Stdout
	tty.Stderr = h.Stderr
	if err := tty.Run(); err != nil {
		return fmt.Errorf("%s: %w", h.Addr, err)
	}
	return nil
}

// Capture runs script as the login user and returns its standard output.
func (h Host) Capture(ctx context.Context, script string) (string, error) {
	if h.DryRun {
		return "", h.print("capture", script)
	}
	var out bytes.Buffer
	cmd := h.command(ctx, false, "bash -s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = &out
	cmd.Stderr = h.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", h.Addr, err)
	}
	return strings.TrimSpace(out.String()), nil
}

// Wait blocks until the host answers ssh. A fresh install reboots at least
// once, so anything that follows a reboot needs this.
func (h Host) Wait(ctx context.Context) error {
	if h.DryRun {
		return nil
	}
	const between = 5 * time.Second
	for {
		cmd := h.command(ctx, false, "true")
		if err := cmd.Run(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", h.Addr, ctx.Err())
		case <-time.After(between):
		}
	}
}

func (h Host) command(ctx context.Context, tty bool, remote string) *exec.Cmd {
	args := []string{"-o", "ConnectTimeout=" + probeTimeout}
	if tty {
		args = append(args, "-t")
	}
	args = append(args, h.Addr, remote)
	return exec.CommandContext(ctx, "ssh", args...)
}

func (h Host) print(what, script string) error {
	_, err := fmt.Fprintf(h.Stdout, "would %s on %s:\n%s\n", what, h.Addr, script)
	return err
}
