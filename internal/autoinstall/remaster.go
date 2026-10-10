package autoinstall

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// grubPath is where the live installer's GRUB configuration sits inside the
// ISO. Both the BIOS and the EFI loader read this one file.
const grubPath = "/boot/grub/grub.cfg"

// KeyFile holds the LUKS passphrase on its own inside the seed directory.
// Subiquity redacts the passphrase from the copy of the seed it leaves on the
// installed system, so the TPM enrollment cannot read it back from there.
const KeyFile = "luks.key"

// Remaster writes a copy of the source ISO that carries the seed and boots
// straight into the install.
//
// The seed goes inside the image rather than onto a second partition, because
// a partition alone does not skip Subiquity's confirmation prompt — that needs
// the `autoinstall` kernel parameter, which needs the image patched anyway.
// One artifact also means `den usb write` is a plain block copy.
func Remaster(ctx context.Context, srcISO, outISO string, seed Seed, timeout int) error {
	if _, err := exec.LookPath("xorriso"); err != nil {
		return fmt.Errorf("xorriso is not installed: brew install xorriso")
	}

	work, err := os.MkdirTemp("", "den-usb-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	patched, err := patchedGrub(ctx, srcISO, work, timeout)
	if err != nil {
		return err
	}
	seedDir, err := writeSeed(work, seed)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(outISO), 0o755); err != nil {
		return err
	}
	// xorriso refuses an -outdev that already holds data, and a stale image
	// left in place is one you write to a stick without noticing.
	if err := os.Remove(outISO); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing the previous image: %w", err)
	}
	// `-boot_image any replay` carries the original El Torito and EFI boot
	// records over to the new image. Rebuilding them by hand is how a
	// remastered ISO ends up unbootable.
	cmd := exec.CommandContext(ctx, "xorriso",
		"-indev", srcISO,
		"-outdev", outISO,
		"-boot_image", "any", "replay",
		"-overwrite", "on",
		"-map", patched, grubPath,
		"-map", seedDir, "/"+SeedDir,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("xorriso: %w\n%s", err, out)
	}
	return nil
}

// patchedGrub extracts the live installer's GRUB configuration, adds the
// autoinstall parameters, and returns the path to the patched copy.
func patchedGrub(ctx context.Context, srcISO, work string, timeout int) (string, error) {
	extracted := filepath.Join(work, "grub.cfg.orig")
	cmd := exec.CommandContext(ctx, "xorriso",
		"-osirrox", "on",
		"-indev", srcISO,
		"-extract", grubPath, extracted,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf(
			"xorriso could not read %s from %s: %w\n%s",
			grubPath,
			srcISO,
			err,
			out,
		)
	}

	cfg, err := os.ReadFile(extracted)
	if err != nil {
		return "", err
	}
	out, err := PatchGrub(string(cfg), timeout)
	if err != nil {
		return "", err
	}

	patched := filepath.Join(work, "grub.cfg")
	if err := os.WriteFile(patched, []byte(out), 0o644); err != nil {
		return "", err
	}
	return patched, nil
}

// writeSeed renders user-data and meta-data into a directory for xorriso to
// map onto the image.
func writeSeed(work string, seed Seed) (string, error) {
	dir := filepath.Join(work, SeedDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	userData, err := seed.UserData()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "user-data"), userData, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(
		filepath.Join(dir, "meta-data"),
		MetaData(seed.Hostname),
		0o644,
	); err != nil {
		return "", err
	}
	// No trailing newline: systemd-cryptenroll --unlock-key-file takes the
	// whole file as the passphrase.
	if err := os.WriteFile(
		filepath.Join(dir, KeyFile),
		[]byte(seed.LUKSPassphrase),
		0o600,
	); err != nil {
		return "", err
	}
	return dir, nil
}
