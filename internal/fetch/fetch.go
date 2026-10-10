// Package fetch downloads a file over HTTP and checksums it. The autoinstall
// ISO and the memtest86+ image both come down this path.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Download writes url to path, through a temporary file in the same directory
// so an interrupted transfer never looks like a complete one. onProgress may
// be nil.
func Download(ctx context.Context, url, path string, onProgress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &countingReader{r: resp.Body, total: resp.ContentLength, fn: onProgress}
	}
	if _, err := tmp.ReadFrom(src); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// SHA256 is the hex digest of a file on disk.
func SHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := f.WriteTo(h); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// countingReader reports how much has been read. total is -1 when the server
// sends no Content-Length.
type countingReader struct {
	r     io.Reader
	total int64
	done  int64
	fn    func(done, total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	c.fn(c.done, c.total)
	return n, err
}
