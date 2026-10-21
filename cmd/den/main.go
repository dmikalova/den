// Command den is the operational toolkit for the den home server, and the
// tool that builds its install media. The vocabulary is in CONTEXT.md.
//
// Usage:
//
//	den usb build [flags]
//	den usb memtest [flags]
//	den usb write --device /dev/diskN [flags]
//	den bootstrap [flags]
//	den apply [flags]
//	den check [flags]
//	den notify [flags] [message]
//	den version
//	den help
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// version is the release version, set by goreleaser with -ldflags.
var version = "dev"

// errUsage marks an error whose fix is reading the usage.
var errUsage = errors.New("usage")

func main() {
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(stop, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var err error
	switch {
	case len(args) == 0, args[0] == "help", args[0] == "-h", args[0] == "--help":
		usage(stdout)
		return 0
	case args[0] == "version":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case args[0] == "usb":
		err = usb(ctx, args[1:], stdout, stderr)
	case args[0] == "bootstrap":
		err = bootstrapHost(ctx, args[1:], stdout, stderr)
	case args[0] == "apply":
		err = applyHost(ctx, args[1:], stdout, stderr)
	case args[0] == "check":
		err = checkHost(ctx, args[1:], stdout, stderr)
	case args[0] == "notify":
		err = notifyHost(ctx, args[1:], stdout, stderr)
	case args[0] == "ping":
		err = pingHost(ctx, args[1:], stdout, stderr)
	case args[0] == "secrets":
		err = secretsHost(ctx, args[1:], stdout, stderr)
	default:
		err = fmt.Errorf("unknown command %q: %w", args[0], errUsage)
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "den: %v\n", err)
		if errors.Is(err, errUsage) {
			usage(stderr)
		}
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `den builds den's install media and runs its operational jobs.

Usage:
  den usb build [flags]              build an unattended install image
  den usb memtest [flags]            fetch the memtest86+ image
  den usb write --device <node>      write an image to a removable disk
  den bootstrap [flags]              patch a fresh install and install mgmt
  den apply [flags]                  converge den's root layer with mgmt
  den check [flags]                  run the smoke checks against a host
  den notify [flags] [message]       report an event to Discord, from den
  den ping [flags] <check>           feed a dead-man's switch, from den
  den secrets fetch [flags]          explode den's runtime secret into /run
  den secrets seal [flags]           seal a service account key to the TPM
  den version
  den help

Run `+"`den usb build -h`"+` for the flags of a subcommand.
`)
}
