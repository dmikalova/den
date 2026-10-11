package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dmikalova/den/internal/notify"
)

// These live on den, not in the repository. The webhook is a bearer credential
// and the healthcheck URL identifies the check, so both are secrets.
const (
	defaultWebhookFile = "/etc/den/discord-webhook"
	defaultPingFile    = "/etc/den/healthcheck-url"
)

// journalLines is how much context travels with a failure. Discord cuts an
// embed description at 4096 characters, so this stays small enough to survive.
const journalLines = 15

// notifyHost reports an event to Discord. Unlike den's other subcommands this
// one runs on den itself, driven by systemd's OnFailure=, so it reads the local
// journal and never opens an ssh connection.
func notifyHost(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den notify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	unit := fs.String("unit", "", "systemd unit to report on, with its recent journal")
	level := fs.String("level", "", "info, warn or error (default error with -unit, else info)")
	title := fs.String("title", "", "embed title (default derived from -unit)")
	webhookFile := fs.String(
		"webhook-file",
		defaultWebhookFile,
		"file holding the Discord webhook URL",
	)
	pingFile := fs.String(
		"ping-file",
		defaultPingFile,
		"file holding a healthcheck URL to ping on success",
	)
	lines := fs.Int("lines", journalLines, "journal lines to include with -unit")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("den notify: %w", errUsage)
	}

	lvl := notify.Level(*level)
	switch {
	case *level == "" && *unit != "":
		lvl = notify.LevelError
	case *level == "":
		lvl = notify.LevelInfo
	case !lvl.Valid():
		return fmt.Errorf("unknown level %q: %w", *level, errUsage)
	}

	body := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if body == "" && *unit == "" {
		return fmt.Errorf("den notify needs a message or -unit: %w", errUsage)
	}

	msg := notify.Message{
		Title: *title,
		Body:  body,
		Level: lvl,
		Host:  hostname(),
	}
	if *unit != "" {
		describeUnit(ctx, &msg, *unit, *lines)
	}

	webhook, err := readSecret(*webhookFile)
	if err != nil {
		return err
	}
	if err := notify.Send(ctx, webhook, msg); err != nil {
		return fmt.Errorf("sending the alert: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "alert sent")

	// A missing healthcheck URL is not an error: most alerts are not jobs.
	// A present but failing one is worth saying out loud, but it must not fail
	// the command, because the alert itself already landed.
	ping, err := readSecret(*pingFile)
	if err != nil {
		return nil
	}
	if err := notify.Ping(ctx, ping); err != nil {
		_, _ = fmt.Fprintf(stderr, "den: the healthcheck ping failed: %v\n", err)
	}
	return nil
}

// describeUnit fills in what systemd knows about a failed unit. Every failure
// here is tolerated: a half-described alert beats no alert.
func describeUnit(ctx context.Context, msg *notify.Message, unit string, lines int) {
	if msg.Title == "" {
		msg.Title = unit + " failed"
	}

	props := showUnit(ctx, unit, "Result", "ExecMainStatus", "ActiveState")
	for _, name := range []string{"ActiveState", "Result", "ExecMainStatus"} {
		if v := props[name]; v != "" {
			msg.Fields = append(msg.Fields, notify.Field{Name: name, Value: v, Inline: true})
		}
	}

	if log := journal(ctx, unit, lines); log != "" {
		// A code fence keeps Discord from reading log text as markdown.
		msg.Body = strings.TrimSpace(msg.Body + "\n```\n" + log + "\n```")
	}
}

// showUnit asks systemd for named properties. The unit name arrives from
// systemd's own %N specifier and is passed as an argument, never through a
// shell, so it cannot inject a command.
func showUnit(ctx context.Context, unit string, props ...string) map[string]string {
	args := []string{"show", unit}
	for _, p := range props {
		args = append(args, "--property="+p)
	}
	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return nil
	}

	values := make(map[string]string, len(props))
	for line := range strings.SplitSeq(string(bytes.TrimSpace(out)), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if ok && value != "" {
			values[name] = value
		}
	}
	return values
}

func journal(ctx context.Context, unit string, lines int) string {
	out, err := exec.CommandContext(ctx, "journalctl",
		"--unit", unit,
		"--lines", strconv.Itoa(lines),
		"--no-pager",
		"--output", "short-iso",
	).Output()
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(out))
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// readSecret reads a one-line credential from a file. The content is never
// echoed, and neither is the path's content on failure.
func readSecret(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	s := string(bytes.TrimSpace(b))
	if s == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return s, nil
}
