package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dmikalova/den/internal/autoinstall"
	"github.com/dmikalova/den/internal/progress"
	"github.com/dmikalova/den/internal/secrets"
	"github.com/dmikalova/den/internal/usbdisk"
)

// defaultRelease is the Ubuntu Server release den installs.
const defaultRelease = "26.04.1"

// defaultSecrets holds the console password hash and the LUKS passphrase.
const defaultSecrets = "secrets/install.sops.json"

func usb(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usb needs a subcommand, build or write: %w", errUsage)
	}
	switch args[0] {
	case "build":
		return usbBuild(ctx, args[1:], stdout)
	case "write":
		return usbWrite(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown usb subcommand %q: %w", args[0], errUsage)
	}
}

func usbBuild(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("den usb build", flag.ContinueOnError)
	var (
		iso      = fs.String("iso", "", "source ISO; downloaded into --cache when empty")
		release  = fs.String("release", defaultRelease, "Ubuntu Server release")
		cache    = fs.String("cache", defaultCache(), "where source ISOs are kept")
		out      = fs.String("out", defaultOut(), "image to write")
		hostname = fs.String("hostname", "den", "host name of the installed system")
		user     = fs.String("user", "dmikalova", "the one login account")
		keyPath  = fs.String("ssh-key", defaultKey(), "OpenSSH public key allowed to log in")
		secrets  = fs.String("secrets", defaultSecrets, "SOPS file holding the install secrets")
		passPath = fs.String(
			"password-file",
			"",
			"file holding the console password; overrides --secrets",
		)
		luksPath = fs.String(
			"luks-passphrase-file",
			"",
			"file holding the LUKS passphrase; overrides --secrets",
		)
		timeout = fs.Int("menu-timeout", 1, "seconds GRUB waits before it starts the install")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	keys, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	install, err := loadInstallSecrets(ctx, *secrets, *passPath, *luksPath)
	if err != nil {
		return err
	}
	hash, err := autoinstall.HashPassword(install.Password)
	if err != nil {
		return err
	}
	seed := autoinstall.Seed{
		Hostname:       *hostname,
		Username:       *user,
		PasswordHash:   hash,
		AuthorizedKeys: keys,
		LUKSPassphrase: install.LUKSPassphrase,
	}
	if err := seed.Validate(); err != nil {
		return err
	}

	src := *iso
	if src == "" {
		_, _ = fmt.Fprintf(stdout, "fetching Ubuntu Server %s\n", *release)
		p := progress.New(stdout, "download")
		src, err = autoinstall.Fetch(ctx, *release, *cache, p.Update)
		p.Done()
		if err != nil {
			return err
		}
	} else if err := autoinstall.VerifyISO(ctx, src, *release); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "source  %s\n", src)

	_, _ = fmt.Fprintf(stdout, "remastering\n")
	if err := autoinstall.Remaster(ctx, src, *out, seed, *timeout); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(
		stdout,
		"image   %s\n\nWrite it with:\n  sudo den usb write --image %s --device /dev/diskN --yes\n",
		*out,
		*out,
	)
	return nil
}

func usbWrite(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("den usb write", flag.ContinueOnError)
	var (
		image  = fs.String("image", defaultOut(), "image to write")
		device = fs.String("device", "", "whole removable disk, such as /dev/disk4")
		yes    = fs.Bool("yes", false, "erase the disk without asking")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *device == "" {
		return fmt.Errorf(
			"usb write needs --device: run `diskutil list external` to find it: %w",
			errUsage,
		)
	}

	stat, err := os.Stat(*image)
	if err != nil {
		return err
	}
	disk, err := usbdisk.Describe(ctx, *device)
	if err != nil {
		return err
	}
	if err := disk.Writable(stat.Size()); err != nil {
		return fmt.Errorf("refusing to write: %w", err)
	}

	_, _ = fmt.Fprintf(
		stdout,
		"disk    %s  %s  %.1f GB\n",
		disk.Node,
		disk.Name,
		float64(disk.Size)/1e9,
	)
	if !*yes {
		return fmt.Errorf("this erases %s: pass --yes to go ahead", disk.Node)
	}

	p := progress.New(stderr, "writing")
	err = usbdisk.Write(ctx, *image, disk, p.Update)
	p.Done()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "written and verified\n")
	return nil
}

// readKey reads the public key that the first SSH session uses. A private key
// here would be published on the install media, so refuse one outright.
func readKey(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("no --ssh-key, and no key at %s", defaultKey())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var keys []string
	scan := bufio.NewScanner(bytes.NewReader(b))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "PRIVATE KEY") {
			return nil, fmt.Errorf("%s is a private key: pass the .pub file", path)
		}
		keys = append(keys, line)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s holds no public key", path)
	}
	return keys, scan.Err()
}

// loadInstallSecrets prefers the explicit files, and only decrypts the SOPS
// file for whatever they do not supply.
func loadInstallSecrets(
	ctx context.Context,
	sopsPath, passPath, luksPath string,
) (secrets.Install, error) {
	var install secrets.Install
	var err error

	if passPath == "" || luksPath == "" {
		if install, err = secrets.Load(ctx, sopsPath); err != nil {
			return install, err
		}
	}
	if passPath != "" {
		if install.Password, err = readTrimmed(passPath); err != nil {
			return install, err
		}
	}
	if luksPath != "" {
		if install.LUKSPassphrase, err = readTrimmed(luksPath); err != nil {
			return install, err
		}
	}
	return install, nil
}

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(b)), nil
}

func defaultKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_dmikalova.pub", "id_ed25519.pub", "id_ecdsa.pub"} {
		path := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func defaultCache() string {
	return inDownloads("")
}

func defaultOut() string {
	return inDownloads("den-autoinstall.iso")
}

// inDownloads keeps the ISOs somewhere visible. They are several GB each and a
// cache directory is where they would be forgotten.
func inDownloads(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return name
	}
	return filepath.Join(home, "Downloads", name)
}
