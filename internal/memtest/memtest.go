// Package memtest downloads the memtest86+ boot image. The image tests all of
// RAM, including the memory a running kernel occupies, so it cannot be a
// package installed on the host under test.
package memtest

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dmikalova/den/internal/fetch"
)

// DefaultVersion is the memtest86+ release den fetches.
const DefaultVersion = "8.10"

// downloadURL is where the project publishes its releases.
const downloadURL = "https://www.memtest.org/download"

// archiveName is the x86_64 hybrid ISO archive, as published. The ISO carries
// both an MBR and an EFI system partition, so the same bytes boot either way.
func archiveName(version string) string {
	return fmt.Sprintf("mt86plus_%s_x86_64.iso.zip", version)
}

// ImageName is what den calls the unpacked image. The archive always holds a
// file called memtest.iso, which says nothing about its version.
func ImageName(version string) string {
	return fmt.Sprintf("memtest86plus-%s.iso", version)
}

// Fetch downloads memtest86+ into dir, checks it against the published
// checksum list, unpacks the ISO and returns its path. An image already
// unpacked in dir is returned as is. onProgress may be nil.
//
// Unlike the Ubuntu ISO, memtest86+ publishes no signature over its checksum
// list. Both files come from the same host, so the checksum proves the
// download is intact, not that the host is honest.
func Fetch(
	ctx context.Context,
	version, dir string,
	onProgress func(done, total int64),
) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	image := filepath.Join(dir, ImageName(version))
	if _, err := os.Stat(image); err == nil {
		return image, nil
	}

	base := fmt.Sprintf("%s/v%s", downloadURL, version)
	sums := filepath.Join(dir, fmt.Sprintf("memtest86plus-%s.sha256sum.txt", version))
	if err := fetch.Download(ctx, base+"/sha256sum.txt", sums, nil); err != nil {
		return "", err
	}

	archive := filepath.Join(dir, archiveName(version))
	if err := fetch.Download(ctx, base+"/"+archiveName(version), archive, onProgress); err != nil {
		return "", err
	}

	// The checksum list names each file by its release directory.
	want, err := sumFor(sums, fmt.Sprintf("v%s/%s", version, archiveName(version)))
	if err != nil {
		return "", err
	}
	got, err := fetch.SHA256(archive)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf(
			"%s has checksum %s, want %s: delete it and try again",
			archive,
			got,
			want,
		)
	}

	if err := unpack(archive, image); err != nil {
		return "", err
	}
	return image, nil
}

// unpack writes the single ISO inside a memtest86+ archive to dst.
func unpack(archive, dst string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		if !strings.HasSuffix(f.Name, ".iso") {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()

		out, err := os.CreateTemp(filepath.Dir(dst), ".unpack-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(out.Name()) }()

		if _, err := out.ReadFrom(src); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Rename(out.Name(), dst)
	}
	return fmt.Errorf("%s holds no .iso: the archive layout changed", archive)
}

// sumFor reads one entry out of a sha256sum list.
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
	return "", fmt.Errorf("%s is not listed in %s: check the version number", name, sums)
}
