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
//
// onProgress, if not nil, is called after each read with the bytes
// received so far and the total, which is -1 when the server doesn't say.
// It's called on Download's goroutine, so it should be quick.
func Download(ctx context.Context, c *http.Client, url, dir, wantSHA256 string, onProgress Progress) (path, sum string, err error) {
	return download(ctx, c, url, dir, wantSHA256, onProgress, limits{size: maxDownloadSize, stall: stallTimeout})
}

// Progress receives a download's byte counts: done so far, of total (-1
// when unknown).
type Progress func(done, total int64)

// limits are Download's limits, smaller in tests.
type limits struct {
	size  int64
	stall time.Duration
}

func download(ctx context.Context, c *http.Client, url, dir, wantSHA256 string, onProgress Progress, lim limits) (path, sum string, err error) {
	if wantSHA256 != "" && !IsSHA256(wantSHA256) {
		// The source's data is wrong, not the file, so this isn't
		// ErrChecksum.
		return "", "", fmt.Errorf("download %s: expected sha256 %q isn't 64 hex digits", url, wantSHA256)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// wrap adds the URL, and reports why the request was cancelled rather
	// than the bare "context canceled" the transport returns.
	wrap := func(err error) error {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		return fmt.Errorf("download %s: %w", url, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", wrap(err)
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
		return "", "", wrap(err)
	}
	f, err := os.CreateTemp(dir, "download-*.zip")
	if err != nil {
		return "", "", wrap(err)
	}
	defer func() {
		if err != nil {
			_ = f.Close() // may already be closed; the file goes anyway
			_ = os.Remove(f.Name())
		}
	}()

	// Cancel with ErrStalled if the body stops arriving. The timer starts
	// once headers are in, so connecting and retries don't count against
	// it (they have their own timeouts), and is reset after every read
	// and write, so a slow disk doesn't count either.
	stall := time.AfterFunc(lim.stall, func() { cancel(ErrStalled) })
	defer stall.Stop()
	alive := func() { stall.Reset(lim.stall) }
	var done int64
	read := func(n int) {
		alive()
		done += int64(n)
		if onProgress != nil {
			onProgress(done, resp.ContentLength)
		}
	}

	h := sha256.New()
	body := &progressReader{r: io.LimitReader(resp.Body, lim.size+1), onRead: read}
	dst := &progressWriter{w: io.MultiWriter(f, h), onWrite: alive}
	n, err := io.Copy(dst, body)
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
		return "", "", wrap(err)
	}

	sum = hex.EncodeToString(h.Sum(nil))
	if wantSHA256 != "" && !strings.EqualFold(sum, wantSHA256) {
		return "", "", fmt.Errorf("download %s: %w: got sha256 %s, want %s", url, ErrChecksum, sum, strings.ToLower(wantSHA256))
	}
	return f.Name(), sum, nil
}

// IsSHA256 reports whether s is a SHA-256 in hex, in either case. Sources
// use it to check the digests they publish, so both sides agree.
func IsSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// progressReader calls onRead with the size of every read that returns
// data.
type progressReader struct {
	r      io.Reader
	onRead func(n int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.onRead(n)
	}
	return n, err
}

// progressWriter calls onWrite after every write.
type progressWriter struct {
	w       io.Writer
	onWrite func()
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.onWrite()
	return n, err
}
