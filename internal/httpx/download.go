package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Errors from Download. Match them with errors.Is.
var (
	// ErrChecksum means the file's SHA-256 isn't the one the source
	// published: the download is corrupt or not the release it claims.
	ErrChecksum = errors.New("checksum mismatch")
	// ErrStalled means the server stopped sending the body.
	ErrStalled = errors.New("download stalled")
	// ErrTooLarge means the body is bigger than any addon should be.
	ErrTooLarge = errors.New("download too large")
)

// StatusError is a response other than 200 OK.
type StatusError struct {
	URL        string
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("download %s: %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
}

// Limits on a download. Addons are at most tens of megabytes; the size
// limit keeps a wrong URL from filling the disk.
const (
	maxDownloadSize = 1 << 30
	stallTimeout    = 60 * time.Second
)

// Download streams url into a new file in dir, verifying wantSHA256 when
// it's non-empty, and returns the file's path and actual SHA-256 as
// lowercase hex. The caller removes the file when done with it. On error,
// no file is left behind.
//
// The body is hashed as it's written, so the file is read only once. A body
// shorter than its Content-Length, or one that stops arriving for a
// minute, is an error.
func Download(ctx context.Context, c *http.Client, url, dir, wantSHA256 string) (path, sum string, err error) {
	return download(ctx, c, url, dir, wantSHA256, limits{size: maxDownloadSize, stall: stallTimeout})
}

// limits are Download's limits, smaller in tests.
type limits struct {
	size  int64
	stall time.Duration
}

func download(ctx context.Context, c *http.Client, url, dir, wantSHA256 string, lim limits) (path, sum string, err error) {
	// Cancel the request with ErrStalled if no data arrives for a while.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(lim.stall, func() { cancel(ErrStalled) })
	defer stall.Stop()
	// wrap reports why the request was cancelled, rather than the bare
	// "context canceled" the transport returns.
	wrap := func(err error) error {
		if cause := context.Cause(ctx); cause != nil {
			return fmt.Errorf("download %s: %w", url, cause)
		}
		return fmt.Errorf("download %s: %w", url, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", url, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", "", wrap(err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush
	if resp.StatusCode != http.StatusOK {
		return "", "", &StatusError{URL: url, StatusCode: resp.StatusCode}
	}
	if resp.ContentLength > lim.size {
		return "", "", fmt.Errorf("download %s: %w (%d bytes)", url, ErrTooLarge, resp.ContentLength)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp(dir, "download-*.zip")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = f.Close() // may already be closed; the file goes anyway
			_ = os.Remove(f.Name())
		}
	}()

	h := sha256.New()
	body := &progressReader{r: io.LimitReader(resp.Body, lim.size+1), onRead: func() { stall.Reset(lim.stall) }}
	n, err := io.Copy(io.MultiWriter(f, h), body)
	if err != nil {
		return "", "", wrap(err)
	}
	if n > lim.size {
		return "", "", fmt.Errorf("download %s: %w (over %d bytes)", url, ErrTooLarge, lim.size)
	}
	// The transport reports a short body as io.ErrUnexpectedEOF; this
	// catches any case it doesn't.
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return "", "", fmt.Errorf("download %s: got %d of %d bytes: %w", url, n, resp.ContentLength, io.ErrUnexpectedEOF)
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}

	sum = hex.EncodeToString(h.Sum(nil))
	if wantSHA256 != "" && !strings.EqualFold(sum, wantSHA256) {
		return "", "", fmt.Errorf("download %s: %w: got sha256 %s, want %s", url, ErrChecksum, sum, strings.ToLower(wantSHA256))
	}
	return f.Name(), sum, nil
}

// progressReader calls onRead after every read that returns data.
type progressReader struct {
	r      io.Reader
	onRead func()
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.onRead()
	}
	return n, err
}
