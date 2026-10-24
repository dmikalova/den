package autoinstall

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dmikalova/den/v3/internal/fetch"
)

// releasesURL is where Canonical publishes the Server ISO, its checksums and
// the signature over those checksums.
const releasesURL = "https://releases.ubuntu.com"

// ISOName is the Server ISO file name for a release, as published.
func ISOName(release string) string {
	return fmt.Sprintf("ubuntu-%s-live-server-amd64.iso", release)
}

// Fetch downloads the Server ISO for a release into dir and verifies it
// against Canonical's signed checksum list. It returns the ISO path. An
// already downloaded ISO is verified, not downloaded again. onProgress may be
// nil; it is called only for the ISO itself.
func Fetch(
	ctx context.Context,
	release, dir string,
	onProgress func(done, total int64),
) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	base := fmt.Sprintf("%s/%s", releasesURL, release)
	iso := filepath.Join(dir, ISOName(release))
	sums := filepath.Join(dir, "SHA256SUMS")
	sig := filepath.Join(dir, "SHA256SUMS.gpg")

	for _, f := range []struct{ url, path string }{
		{base + "/SHA256SUMS", sums},
		{base + "/SHA256SUMS.gpg", sig},
	} {
		if err := fetch.Download(ctx, f.url, f.path, nil); err != nil {
			return "", err
		}
	}
	if err := verifySignature(ctx, sums, sig); err != nil {
		return "", err
	}

	want, err := sumFor(sums, ISOName(release))
	if err != nil {
		return "", err
	}
	if got, err := fetch.SHA256(iso); err == nil && got == want {
		return iso, nil
	}
	if err := fetch.Download(ctx, base+"/"+ISOName(release), iso, onProgress); err != nil {
		return "", err
	}

	got, err := fetch.SHA256(iso)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf(
			"%s has checksum %s, want %s: delete it and try again",
			iso,
			got,
			want,
		)
	}
	return iso, nil
}

// VerifyISO checks an ISO the caller downloaded by hand against the signed
// checksum list for its release.
func VerifyISO(ctx context.Context, isoPath, release string) error {
	dir := filepath.Dir(isoPath)
	sums := filepath.Join(dir, "SHA256SUMS")
	sig := filepath.Join(dir, "SHA256SUMS.gpg")
	base := fmt.Sprintf("%s/%s", releasesURL, release)

	for _, f := range []struct{ url, path string }{
		{base + "/SHA256SUMS", sums},
		{base + "/SHA256SUMS.gpg", sig},
	} {
		if err := fetch.Download(ctx, f.url, f.path, nil); err != nil {
			return err
		}
	}
	if err := verifySignature(ctx, sums, sig); err != nil {
		return err
	}

	want, err := sumFor(sums, filepath.Base(isoPath))
	if err != nil {
		return err
	}
	got, err := fetch.SHA256(isoPath)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s has checksum %s, want %s", isoPath, got, want)
	}
	return nil
}

// verifySignature checks the checksum list against Canonical's signing key.
// A checksum alone proves only that the download matched whatever the mirror
// served; the signature is what makes it trustworthy.
func verifySignature(ctx context.Context, sums, sig string) error {
	if _, err := exec.LookPath("gpg"); err != nil {
		return fmt.Errorf(
			"gpg is not installed, so the ISO checksums cannot be verified: brew install gnupg",
		)
	}
	out, err := exec.CommandContext(ctx, "gpg", "--verify", sig, sums).CombinedOutput()
	if err != nil {
		return fmt.Errorf("the ISO checksums are not signed by a key you trust: %w\n%s\n"+
			"Import Canonical's key, then try again:\n"+
			"  gpg --keyserver keyserver.ubuntu.com --recv-keys 843938DF228D22F7B3742BC0D94AA3F0EFE21092",
			err, out)
	}
	return nil
}

// sumFor reads one entry out of a SHA256SUMS list.
func sumFor(sums, name string) (string, error) {
	f, err := os.Open(sums)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	if err := scan.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s is not listed in %s: check the release number", name, sums)
}
