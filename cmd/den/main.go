// Command den is the operational toolkit for the den home server, and the
// tool that builds its install media. The vocabulary is in CONTEXT.md.
//
// Usage:
//
//	den usb build [flags]
//	den usb write --device /dev/diskN [flags]
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
  den usb write --device <node>      write an image to a removable disk
  den version
  den help

Run `+"`den usb build -h`"+` for the flags of a subcommand.
`)
}
