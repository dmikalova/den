package usbdisk

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestWritable(t *testing.T) {
	ok := Disk{Node: "/dev/disk4", Size: 32 << 30, Removable: true, WholeDisk: true}

	cases := []struct {
		name string
		disk Disk
		ok   bool
	}{
		{"removable whole disk", ok, true},
		{"internal", func() Disk { d := ok; d.Internal = true; return d }(), false},
		{"not removable", func() Disk { d := ok; d.Removable = false; return d }(), false},
		{"a partition", func() Disk { d := ok; d.WholeDisk = false; return d }(), false},
		{"too small", func() Disk { d := ok; d.Size = 1 << 20; return d }(), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.disk.Writable(3 << 30)
			if tc.ok && err != nil {
				t.Errorf("Writable: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestCopyBlocksPadsToSectorSize(t *testing.T) {
	src := bytes.Repeat([]byte{0xAB}, blockSize+100)
	var dst bytes.Buffer

	done, err := copyBlocks(t.Context(), bytes.NewReader(src), &dst, int64(len(src)), nil)
	if err != nil {
		t.Fatalf("copyBlocks: %v", err)
	}
	if done != int64(len(src)) {
		t.Errorf("reported %d bytes, want %d", done, len(src))
	}
	if dst.Len()%2048 != 0 {
		t.Errorf("wrote %d bytes, which is not sector-aligned", dst.Len())
	}
	if !bytes.Equal(dst.Bytes()[:len(src)], src) {
		t.Error("the written bytes do not match the source")
	}
}

func TestCopyBlocksStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	src := bytes.Repeat([]byte{0xAB}, blockSize*4)
	var dst bytes.Buffer

	_, err := copyBlocks(ctx, bytes.NewReader(src), &dst, int64(len(src)), nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err is %v, want context.Canceled", err)
	}
	if dst.Len() != 0 {
		t.Errorf("wrote %d bytes after cancellation, want 0", dst.Len())
	}
}
