// Package usbdisk writes an image to a removable disk. It refuses anything
// that is not removable: the cost of a wrong device node here is the
// contents of the laptop.
package usbdisk

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// blockSize is the copy unit. macOS raw devices reject writes that are not a
// multiple of the sector size, so this must stay sector-aligned.
const blockSize = 4 << 20

// Disk is what the system reports about a whole disk.
type Disk struct {
	Node       string
	Name       string
	Size       int64
	Internal   bool
	Removable  bool
	WholeDisk  bool
	Partitions []string
}

// Describe reports what a device node is, so the caller can refuse to write
// to the wrong one.
func Describe(ctx context.Context, node string) (Disk, error) {
	if runtime.GOOS != "darwin" {
		return Disk{}, fmt.Errorf(
			"den usb write runs on macOS only; on Linux use lsblk to confirm the device and dd to write it",
		)
	}

	info, err := exec.CommandContext(ctx, "diskutil", "info", "-plist", node).Output()
	if err != nil {
		return Disk{}, fmt.Errorf("diskutil info %s: %w", node, err)
	}
	out, err := plistJSON(ctx, info)
	if err != nil {
		return Disk{}, err
	}

	var d struct {
		DeviceNode                     string `json:"DeviceNode"`
		MediaName                      string `json:"MediaName"`
		TotalSize                      int64  `json:"TotalSize"`
		Internal                       bool   `json:"Internal"`
		RemovableMediaOrExternalDevice bool   `json:"RemovableMediaOrExternalDevice"`
		WholeDisk                      bool   `json:"WholeDisk"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return Disk{}, fmt.Errorf("reading diskutil output: %w", err)
	}
	return Disk{
		Node:      d.DeviceNode,
		Name:      d.MediaName,
		Size:      d.TotalSize,
		Internal:  d.Internal,
		Removable: d.RemovableMediaOrExternalDevice,
		WholeDisk: d.WholeDisk,
	}, nil
}

// External lists the whole physical disks macOS sees as external. The caller
// still checks each one with Writable before it writes anything.
func External(ctx context.Context) ([]Disk, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("listing disks runs on macOS only")
	}
	plist, err := exec.CommandContext(ctx, "diskutil", "list", "-plist", "external", "physical").
		Output()
	if err != nil {
		return nil, fmt.Errorf("diskutil list: %w", err)
	}
	js, err := plistJSON(ctx, plist)
	if err != nil {
		return nil, err
	}
	names, err := parseWholeDisks(js)
	if err != nil {
		return nil, err
	}
	disks := make([]Disk, 0, len(names))
	for _, n := range names {
		d, err := Describe(ctx, "/dev/"+n)
		if err != nil {
			return nil, err
		}
		disks = append(disks, d)
	}
	return disks, nil
}

func parseWholeDisks(js []byte) ([]string, error) {
	var l struct {
		WholeDisks []string `json:"WholeDisks"`
	}
	if err := json.Unmarshal(js, &l); err != nil {
		return nil, fmt.Errorf("reading diskutil output: %w", err)
	}
	return l.WholeDisks, nil
}

func plistJSON(ctx context.Context, plist []byte) ([]byte, error) {
	conv := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = bytes.NewReader(plist)
	out, err := conv.Output()
	if err != nil {
		return nil, fmt.Errorf("reading diskutil output: %w", err)
	}
	return out, nil
}

// Pick asks which disk to write. Only disks that pass Writable are offered.
// An empty answer skips the write, so ok is false with no error.
func Pick(in io.Reader, out io.Writer, disks []Disk, imageSize int64) (Disk, bool, error) {
	var offer []Disk
	for _, d := range disks {
		if d.Writable(imageSize) == nil {
			offer = append(offer, d)
		}
	}
	if len(offer) == 0 {
		_, _ = fmt.Fprintf(out, "no removable disk large enough is plugged in\n")
		return Disk{}, false, nil
	}

	_, _ = fmt.Fprintf(out, "\nWrite the image to a USB stick? Its contents are erased.\n")
	for i, d := range offer {
		_, _ = fmt.Fprintf(out, "  %d) %s  %s  %.1f GB\n", i+1, d.Node, d.Name, float64(d.Size)/1e9)
	}
	_, _ = fmt.Fprintf(out, "Number, or Enter to skip: ")

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return Disk{}, false, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return Disk{}, false, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(offer) {
		return Disk{}, false, fmt.Errorf("%q is not one of the disks listed", line)
	}
	return offer[n-1], true, nil
}

// Writable reports why a disk must not be written to, or nil if it may be.
func (d Disk) Writable(imageSize int64) error {
	switch {
	case d.Internal:
		return fmt.Errorf("%s is an internal disk", d.Node)
	case !d.Removable:
		return fmt.Errorf("%s is not removable", d.Node)
	case !d.WholeDisk:
		return fmt.Errorf("%s is a partition: give the whole disk, such as /dev/disk4", d.Node)
	case d.Size < imageSize:
		return fmt.Errorf("%s holds %d bytes, and the image is %d", d.Node, d.Size, imageSize)
	}
	return nil
}

// Write copies image onto the disk and reads it back to confirm it landed.
// It unmounts the disk first; macOS holds the raw device open otherwise.
func Write(ctx context.Context, imagePath string, d Disk, progress func(done, total int64)) error {
	img, err := os.Open(imagePath)
	if err != nil {
		return err
	}
	defer func() { _ = img.Close() }()

	stat, err := img.Stat()
	if err != nil {
		return err
	}
	if err := d.Writable(stat.Size()); err != nil {
		return err
	}

	if out, err := exec.CommandContext(ctx, "diskutil", "unmountDisk", d.Node).
		CombinedOutput(); err != nil {
		return fmt.Errorf("unmounting %s: %w\n%s", d.Node, err, out)
	}

	// The raw node skips the buffer cache, which is the difference between a
	// write that takes minutes and one that takes an hour.
	raw := strings.Replace(d.Node, "/dev/disk", "/dev/rdisk", 1)
	dev, err := os.OpenFile(raw, os.O_WRONLY|os.O_SYNC, 0)
	if err != nil {
		return fmt.Errorf("opening %s: %w (run with sudo)", raw, err)
	}

	written, err := copyBlocks(ctx, img, dev, stat.Size(), progress)
	if cerr := dev.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return err
	}

	return verify(ctx, imagePath, raw, written)
}

// copyBlocks copies in sector-aligned units, padding the last one. The
// installer image is already a multiple of 2 KiB, but a short final write to a
// raw device fails outright rather than writing less.
func copyBlocks(
	ctx context.Context,
	src io.Reader,
	dst io.Writer,
	total int64,
	progress func(done, total int64),
) (int64, error) {
	buf := make([]byte, blockSize)
	var done int64

	for {
		// Nothing else observes cancellation once the copy starts, and
		// signal.NotifyContext has already disabled the default SIGINT kill.
		if err := ctx.Err(); err != nil {
			return done, fmt.Errorf("interrupted after %d bytes: %w", done, err)
		}
		n, err := io.ReadFull(src, buf)
		if n > 0 {
			out := buf[:n]
			if pad := n % 2048; pad != 0 {
				out = buf[:n+2048-pad]
			}
			if _, werr := dst.Write(out); werr != nil {
				return done, fmt.Errorf("writing at offset %d: %w", done, werr)
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		switch err {
		case nil:
		case io.EOF, io.ErrUnexpectedEOF:
			return done, nil
		default:
			return done, err
		}
	}
}

// verify reads the disk back and compares it with the image. A USB drive that
// accepts writes and stores none is a common enough failure to check for.
func verify(ctx context.Context, imagePath, raw string, size int64) error {
	want, err := sumOf(ctx, imagePath, size)
	if err != nil {
		return err
	}
	got, err := sumOf(ctx, raw, size)
	if err != nil {
		return fmt.Errorf("reading %s back: %w", raw, err)
	}
	if got != want {
		return fmt.Errorf("the disk does not match the image: it wrote %s, want %s", got, want)
	}
	return nil
}

func sumOf(ctx context.Context, path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	for left := size; left > 0; {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := min(int64(blockSize), left)
		if _, err := io.CopyN(h, f, n); err != nil {
			return "", err
		}
		left -= n
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
