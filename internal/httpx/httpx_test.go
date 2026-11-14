package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const body = "PK fake zip contents"

func sumOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// testClient is NewClient with retries that don't wait.
func testClient() *http.Client {
	c := NewClient("v0.0.0-test")
	c.Transport.(*transport).backoff = time.Millisecond
	return c
}

// serve starts a server running h and returns its URL and a count of the
// requests it got.
func serve(t *testing.T, h http.HandlerFunc) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &calls
}

func ok(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }

// assertEmpty checks that a failed download left nothing behind.
func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("download left %v behind", entries)
	}
}

func TestDownload(t *testing.T) {
	t.Parallel()
	url, _ := serve(t, ok)
	dir := t.TempDir()
	for _, want := range []string{sumOf(body), strings.ToUpper(sumOf(body)), ""} {
		path, sum, err := Download(context.Background(), testClient(), url, dir, want, nil)
		if err != nil {
			t.Fatalf("want %q: %v", want, err)
		}
		if sum != sumOf(body) {
			t.Errorf("sum = %s, want %s", sum, sumOf(body))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != body {
			t.Errorf("file = %q, want %q", data, body)
		}
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	t.Parallel()
	url, _ := serve(t, ok)
	dir := t.TempDir()
	_, _, err := Download(context.Background(), testClient(), url, dir, sumOf("something else"), nil)
	if !errors.Is(err, ErrChecksum) || !strings.Contains(err.Error(), sumOf(body)) {
		t.Errorf("error = %v, want ErrChecksum naming the actual sum", err)
	}
	assertEmpty(t, dir)
}

// A body shorter than its Content-Length is an error, not a short file.
func TestDownloadTruncated(t *testing.T) {
	t.Parallel()
	url, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+100))
		_, _ = io.WriteString(w, body)
	})
	dir := t.TempDir()
	_, _, err := Download(context.Background(), testClient(), url, dir, "", nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("error = %v, want io.ErrUnexpectedEOF", err)
	}
	assertEmpty(t, dir)
}

func TestDownloadStatus(t *testing.T) {
	t.Parallel()
	url, calls := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	dir := t.TempDir()
	_, _, err := Download(context.Background(), testClient(), url, dir, "", nil)
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusNotFound {
		t.Errorf("error = %v, want a 404 StatusError", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("a 404 was tried %d times, want 1", n)
	}
	assertEmpty(t, dir)
}

// Server errors are retried twice, then returned.
func TestRetries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		failures  int32
		wantCalls int32
		wantErr   bool
	}{
		{"recovers after one failure", 1, 2, false},
		{"recovers after two failures", 2, 3, false},
		{"gives up after three failures", 3, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var n atomic.Int32
			url, calls := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if n.Add(1) <= tt.failures {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				ok(w, r)
			})
			_, _, err := Download(context.Background(), testClient(), url, t.TempDir(), "", nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, want error: %v", err, tt.wantErr)
			}
			if got := calls.Load(); got != tt.wantCalls {
				t.Errorf("calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

// Only idempotent requests are retried, and never on a 429.
func TestNoRetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		status int
	}{
		{"post", http.MethodPost, http.StatusInternalServerError},
		{"rate limited", http.MethodGet, http.StatusTooManyRequests},
		{"client error", http.MethodGet, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			url, calls := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })
			req, err := http.NewRequestWithContext(context.Background(), tt.method, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := testClient().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if got := calls.Load(); got != 1 {
				t.Errorf("calls = %d, want 1", got)
			}
		})
	}
}

// fakeTransport fails its first n round trips with err.
type fakeTransport struct {
	n     int
	err   error
	calls int
}

func (f *fakeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	if f.calls <= f.n {
		return nil, f.err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// A reset connection is retried; other network errors aren't.
func TestRetryConnectionReset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err       error
		wantCalls int
	}{
		{&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", resetErrno)}, 2},
		{errors.New("no such host"), 1},
	}
	for _, tt := range tests {
		fake := &fakeTransport{n: 1, err: tt.err}
		tr := &transport{base: fake, userAgent: "knapsack/test", backoff: time.Millisecond}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid/", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := tr.RoundTrip(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		if fake.calls != tt.wantCalls {
			t.Errorf("%v: calls = %d, want %d", tt.err, fake.calls, tt.wantCalls)
		}
	}
}

func TestUserAgent(t *testing.T) {
	t.Parallel()
	got := make(chan string, 1)
	url, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("User-Agent")
		ok(w, r)
	})
	if _, _, err := Download(context.Background(), testClient(), url, t.TempDir(), "", nil); err != nil {
		t.Fatal(err)
	}
	if ua := <-got; ua != "knapsack/v0.0.0-test" {
		t.Errorf("User-Agent = %q, want knapsack/v0.0.0-test", ua)
	}
}

// Cancelling mid-body stops the download and leaves no file.
func TestDownloadCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	url, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
		w.(http.Flusher).Flush()
		cancel()
		<-r.Context().Done() // never finishes on its own
	})
	dir := t.TempDir()
	_, _, err := Download(ctx, testClient(), url, dir, "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	assertEmpty(t, dir)
}

// A body that stops arriving times out with ErrStalled. Data arriving
// resets the timer, so a slow but steady body doesn't.
func TestDownloadStalled(t *testing.T) {
	t.Parallel()
	// Gaps of a tenth of the timeout leave plenty of margin on a busy CI
	// runner, while the whole body takes longer than the timeout.
	const stall = 500 * time.Millisecond
	url, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		for range 12 { // 12 × 50ms is longer than the stall timeout
			_, _ = io.WriteString(w, "x")
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
		if r.URL.Query().Has("hang") {
			<-r.Context().Done()
		}
	})
	lim := limits{size: maxDownloadSize, stall: stall}
	if _, _, err := download(context.Background(), testClient(), url, t.TempDir(), "", nil, lim); err != nil {
		t.Errorf("steady body: %v", err)
	}
	dir := t.TempDir()
	_, _, err := download(context.Background(), testClient(), url+"?hang", dir, "", nil, lim)
	if !errors.Is(err, ErrStalled) {
		t.Errorf("error = %v, want ErrStalled", err)
	}
	assertEmpty(t, dir)
}

func TestDownloadTooLarge(t *testing.T) {
	t.Parallel()
	lim := limits{size: 5, stall: time.Minute}
	for _, chunked := range []bool{false, true} {
		url, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			if chunked {
				w.(http.Flusher).Flush() // no Content-Length
			}
			_, _ = io.WriteString(w, body)
		})
		dir := t.TempDir()
		_, _, err := download(context.Background(), testClient(), url, dir, "", nil, lim)
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("chunked=%v: error = %v, want ErrTooLarge", chunked, err)
		}
		assertEmpty(t, dir)
	}
}

// A GET with http.NoBody is as retryable as one with a nil body.
func TestRetryNoBody(t *testing.T) {
	t.Parallel()
	fake := &fakeTransport{n: 1, err: &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", resetErrno)}}
	tr := &transport{base: fake, userAgent: "knapsack/test", backoff: time.Millisecond}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if fake.calls != 2 {
		t.Errorf("calls = %d, want 2", fake.calls)
	}
}

// A malformed expected hash is the source's mistake, not a corrupt file,
// and nothing is downloaded.
func TestDownloadBadExpectedSum(t *testing.T) {
	t.Parallel()
	url, calls := serve(t, ok)
	for _, want := range []string{"sha256:" + sumOf(body), sumOf(body)[:10], strings.Repeat("z", 64)} {
		_, _, err := Download(context.Background(), testClient(), url, t.TempDir(), want, nil)
		if err == nil || errors.Is(err, ErrChecksum) || !strings.Contains(err.Error(), "isn't 64 hex digits") {
			t.Errorf("want %q: error = %v, want a bad-hash error", want, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("made %d requests, want none", n)
	}
}

// Waiting for headers and retrying don't count as a stalled body.
func TestStallTimerStartsAfterHeaders(t *testing.T) {
	t.Parallel()
	url, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond) // longer than the stall timeout
		ok(w, r)
	})
	lim := limits{size: maxDownloadSize, stall: 100 * time.Millisecond}
	if _, _, err := download(context.Background(), testClient(), url, t.TempDir(), "", nil, lim); err != nil {
		t.Errorf("slow headers: %v", err)
	}
}

// Progress counts every byte, against Content-Length when the server sends
// one and -1 when it streams without.
func TestDownloadProgress(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 100_000)
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantTotal int64
	}{
		{"with length", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(big)))
			_, _ = io.WriteString(w, big)
		}, int64(len(big))},
		{"chunked", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, big[:50_000])
			w.(http.Flusher).Flush() // headers go out before the length is known
			_, _ = io.WriteString(w, big[50_000:])
		}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			url, _ := serve(t, tt.handler)
			var calls int
			var last, total int64
			_, _, err := Download(context.Background(), testClient(), url, t.TempDir(), "", func(done, tot int64) {
				if done < last {
					t.Errorf("progress went backwards: %d after %d", done, last)
				}
				calls++
				last, total = done, tot
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls < 2 || last != int64(len(big)) || total != tt.wantTotal {
				t.Errorf("progress: %d calls, last %d of %d; want several, ending %d of %d", calls, last, total, len(big), tt.wantTotal)
			}
		})
	}
}
