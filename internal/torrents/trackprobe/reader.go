package trackprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var (
	// ErrRateLimited is returned when the file host answers 429.
	ErrRateLimited = errors.New("trackprobe: rate limited")
	// ErrBudgetExceeded is returned when reading the header would need more
	// bytes than allowed.
	ErrBudgetExceeded = errors.New("trackprobe: byte budget exceeded")
	// ErrNoRangeSupport is returned when the host ignores Range requests.
	ErrNoRangeSupport = errors.New("trackprobe: host does not support range requests")
)

// rangeReader is an io.ReadSeeker over a remote file that fetches one chunk at
// a time with "Range: bytes=a-b" and never reads more than budget bytes in
// total. Errors never contain the URL (debrid links carry tokens).
type rangeReader struct {
	ctx    context.Context
	client *http.Client
	url    string

	chunk  int64 // bytes per request
	budget int64 // bytes left

	size     int64 // -1 until known
	pos      int64
	bufStart int64
	buf      []byte

	requests int
	fetched  int64
}

func newRangeReader(ctx context.Context, client *http.Client, link string, chunk, budget int64) *rangeReader {
	if client == nil {
		client = http.DefaultClient
	}
	return &rangeReader{ctx: ctx, client: client, url: link, chunk: chunk, budget: budget, size: -1}
}

func (r *rangeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.size >= 0 && r.pos >= r.size {
		return 0, io.EOF
	}
	if r.pos < r.bufStart || r.pos >= r.bufStart+int64(len(r.buf)) {
		if err := r.fill(r.pos, int64(len(p))); err != nil {
			return 0, err
		}
		if r.pos >= r.bufStart+int64(len(r.buf)) {
			return 0, io.EOF
		}
	}
	n := copy(p, r.buf[r.pos-r.bufStart:])
	r.pos += int64(n)
	return n, nil
}

func (r *rangeReader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.pos + offset
	case io.SeekEnd:
		if r.size < 0 {
			return 0, errors.New("trackprobe: size unknown")
		}
		abs = r.size + offset
	default:
		return 0, errors.New("trackprobe: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("trackprobe: negative position")
	}
	r.pos = abs
	return abs, nil
}

// fill fetches the chunk that starts at off (at least want bytes, up to the
// remaining budget).
func (r *rangeReader) fill(off int64, want int64) error {
	n := r.chunk
	if want > n {
		n = want
	}
	if n > r.budget {
		n = r.budget
	}
	if n <= 0 {
		return ErrBudgetExceeded
	}
	if r.size >= 0 && off+n > r.size {
		n = r.size - off
	}

	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return errors.New("trackprobe: bad request")
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	r.requests++
	resp, err := r.client.Do(req)
	if err != nil {
		return sanitize(err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return io.EOF
	case resp.StatusCode == http.StatusOK:
		// The host ignored Range: only usable for the first bytes.
		if off != 0 {
			return ErrNoRangeSupport
		}
	case resp.StatusCode != http.StatusPartialContent:
		return fmt.Errorf("trackprobe: unexpected status %d", resp.StatusCode)
	}

	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			if total, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
				r.size = total
			}
		}
	}

	buf, err := io.ReadAll(io.LimitReader(resp.Body, n))
	if err != nil {
		return sanitize(err)
	}
	r.budget -= int64(len(buf))
	r.fetched += int64(len(buf))
	r.bufStart = off
	r.buf = buf
	if len(buf) == 0 {
		return io.EOF
	}
	return nil
}

// sanitize drops the URL from net/http errors.
func sanitize(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return fmt.Errorf("trackprobe: request failed: %s", stripURLs(err.Error()))
}

// stripURLs removes anything that looks like a URL from s.
func stripURLs(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if strings.Contains(f, "://") || strings.Contains(f, "token=") {
			fields[i] = "<url>"
		}
	}
	return strings.Join(fields, " ")
}
