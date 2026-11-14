package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"
)

// maxRetries is how many times a request is retried after the first try.
const maxRetries = 2

// NewClient returns the client every source and download shares. It sends
// User-Agent: knapsack/<version>, which identifies the program and nothing
// about the user, and retries idempotent requests at most twice, only on a
// 5xx response or a reset connection.
//
// It has no overall timeout, which would cut off a large download on a
// slow link. Connecting and waiting for response headers time out instead,
// and Download times out a body that stops arriving.
func NewClient(version string) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	base.TLSHandshakeTimeout = 10 * time.Second
	base.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: &transport{
		base:      base,
		userAgent: "knapsack/" + version,
		backoff:   500 * time.Millisecond,
	}}
}

// transport adds the User-Agent and retries. It wraps another
// RoundTripper, which tests replace.
type transport struct {
	base      http.RoundTripper
	userAgent string
	backoff   time.Duration // wait before the first retry; doubles after
}

// RoundTrip implements http.RoundTripper. It doesn't modify req: the
// interface forbids it, so headers go on a clone.
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", t.userAgent)
	}

	retryable := (req.Method == http.MethodGet || req.Method == http.MethodHead) && req.Body == nil
	wait := t.backoff
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if !retryable || attempt == maxRetries || !shouldRetry(resp, err) {
			return resp, err
		}
		if resp != nil {
			_ = resp.Body.Close() // discarded; a retry follows
		}
		if err := sleep(req.Context(), wait); err != nil {
			return nil, err
		}
		wait *= 2
	}
}

// shouldRetry reports whether a result is worth retrying: a server error or
// a reset connection, both usually brief. Anything else, including 429 rate
// limits, which need a much longer wait, is returned as it is.
func shouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		return errors.Is(err, syscall.ECONNRESET)
	}
	return resp.StatusCode >= 500
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
