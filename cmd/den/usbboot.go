package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/dmikalova/den/v3/internal/efiboot"
	"github.com/dmikalova/den/v3/internal/remote"
)

// usbBoot reboots den into the stick one time. The firmware adds a boot entry
// for a stick only when it sees the stick at power-on, so a stick put in while
// den runs has no entry yet. Then den reboots once to let the firmware find
// it, and tries again.
func usbBoot(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den usb boot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host := fs.String("host", defaultHost, "ssh destination, an alias or user@address")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("den usb boot: %w", errUsage)
	}
	h := remote.Host{Addr: *host, Stdout: stdout, Stderr: stderr}

	if !h.Up(ctx) {
		return fmt.Errorf("%s does not answer; select the stick in the firmware boot menu (F11)", h.Addr)
	}
	entry, ok, err := usbEntry(ctx, h)
	if err != nil {
		return err
	}
	if !ok {
		_, _ = fmt.Fprintf(stdout, "den has no boot entry for the stick; "+
			"rebooting so the firmware finds it\n")
		wait, cancel := context.WithTimeout(ctx, rebootTimeout)
		defer cancel()
		if err := rebootAndWait(wait, h, stdout); err != nil {
			return err
		}
		if entry, ok, err = usbEntry(ctx, h); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("the firmware found no USB stick; " +
				"put the stick in den, or select it in the firmware boot menu (F11)")
		}
	}

	_, _ = fmt.Fprintf(stdout, "booting den from Boot%s, %s\n", entry.Num, entry.Label)
	if err := h.Run(ctx, "sudo efibootmgr --bootnext "+entry.Num+" >/dev/null\n"); err != nil {
		return fmt.Errorf("setting BootNext: %w", err)
	}
	// The connection drops mid-command, so its error says nothing.
	_ = h.Run(ctx, "sudo systemctl reboot\n")
	_, err = fmt.Fprintf(stdout, "the install runs unattended. Remove the stick when it "+
		"reboots den, then run `den bootstrap`.\n")
	return err
}

func usbEntry(ctx context.Context, h remote.Host) (efiboot.Entry, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := h.Capture(ctx, "efibootmgr -v\n")
	if err != nil {
		return efiboot.Entry{}, false, fmt.Errorf("listing boot entries: %w", err)
	}
	e, ok := efiboot.USB(efiboot.Parse(out))
	return e, ok, nil
}
