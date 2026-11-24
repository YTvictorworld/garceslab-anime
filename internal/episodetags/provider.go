package episodetags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Format is a provider's response shape.
type Format string

const (
	// FormatJikanV4 is the official Jikan v4 shape (Jikan, Tenrai):
	// {"pagination":{"has_next_page":..},"data":[{"mal_id":1,"filler":..,"recap":..}]}.
	FormatJikanV4 Format = "jikan4"
	// FormatJikanEdge is jikan-edge's shape:
	// {"data":[{"number":1,"filler":..,"recap":..}],"meta":{"pagination":{"hasNextPage":..}}}.
	FormatJikanEdge Format = "edge"
)

// Provider is one Jikan-compatible API.
type Provider struct {
	Name    string
	BaseURL string
	Format  Format
	// MinInterval is the least time between two requests to this provider,
	// chosen to stay well under its published limits.
	MinInterval time.Duration
}

// The default providers, in the default order. Published limits (2026-11):
// Tenrai 4 req/s and 120 req/min; jikan-edge 30 req/10 s and 60 req/min per IP;
// Jikan 3 req/s and 60 req/min. One request every 1 s (Tenrai) or 1.5 s (the
// others) keeps this server at half of the per-minute limits or less.
var (
	ProviderTenrai    = Provider{Name: "tenrai", BaseURL: "https://api.tenrai.org/v1", Format: FormatJikanV4, MinInterval: time.Second}
	ProviderJikanEdge = Provider{Name: "jikan-edge", BaseURL: "https://jikan.lucashdo.com/v1", Format: FormatJikanEdge, MinInterval: 1500 * time.Millisecond}
	ProviderJikan     = Provider{Name: "jikan", BaseURL: "https://api.jikan.moe/v4", Format: FormatJikanV4, MinInterval: 1500 * time.Millisecond}
)

// DefaultProviders returns Tenrai, jikan-edge, then Jikan.
func DefaultProviders() []Provider {
	return []Provider{ProviderTenrai, ProviderJikanEdge, ProviderJikan}
}

// minCustomInterval is used for providers given by URL in ProvidersEnvVar.
const minCustomInterval = 1500 * time.Millisecond

// ParseProviders reads a comma-separated provider list (ProvidersEnvVar). Each
// item is a known name ("tenrai", "jikan-edge", "jikan") or "<format>=<base URL>"
// for another instance, where format is "jikan4" (Jikan v4 shape) or "edge"
// (jikan-edge shape), e.g. "tenrai,jikan4=https://jikan.example.org/v4".
// Unknown items are skipped; an empty result means DefaultProviders.
func ParseProviders(s string) (ret []Provider, skipped []string) {
	known := map[string]Provider{}
	for _, p := range DefaultProviders() {
		known[p.Name] = p
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		var p Provider
		if k, ok := known[strings.ToLower(item)]; ok {
			p = k
		} else if format, base, ok := strings.Cut(item, "="); ok && (format == string(FormatJikanV4) || format == string(FormatJikanEdge)) &&
			(strings.HasPrefix(base, "https://") || strings.HasPrefix(base, "http://")) {
			base = strings.TrimRight(strings.TrimSpace(base), "/")
			p = Provider{Name: base, BaseURL: base, Format: Format(format), MinInterval: minCustomInterval}
		} else {
			skipped = append(skipped, item)
			continue
		}
		if seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		ret = append(ret, p)
	}
	return ret, skipped
}

// sourceEpisode is one episode of a page, in any format.
type sourceEpisode struct {
	Number int
	Filler bool
	Recap  bool
}

type page struct {
	Episodes []sourceEpisode
	HasNext  bool
}

// errKind classifies a failed request.
type errKind int

const (
	// errUnavailable: network error, timeout, 5xx, or a response we can't read.
	errUnavailable errKind = iota
	// errRateLimited: 429 (RetryAfter set when the provider sent one).
	errRateLimited
	// errNotFound: 404, the anime isn't on MAL.
	errNotFound
)

type providerError struct {
	Provider   string
	Kind       errKind
	Status     int
	RetryAfter time.Duration
	Err        error
}

func (e *providerError) Error() string {
	switch e.Kind {
	case errRateLimited:
		return fmt.Sprintf("episodetags: %s: rate limited (retry after %s)", e.Provider, e.RetryAfter)
	case errNotFound:
		return fmt.Sprintf("episodetags: %s: not found", e.Provider)
	}
	if e.Err != nil {
		return fmt.Sprintf("episodetags: %s: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("episodetags: %s: HTTP %d", e.Provider, e.Status)
}

func (e *providerError) Unwrap() error { return e.Err }

func isNotFound(err error) bool {
	var pe *providerError
	return errors.As(err, &pe) && pe.Kind == errNotFound
}

// newHTTPClient fails fast on an unreachable provider (Jikan's TCP connect can
// hang) so the next one is tried quickly.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   2,
		},
	}
}

const maxPageBytes = 4 << 20

// fetchPage requests one episodes page (1-based) of a MAL anime.
func fetchPage(ctx context.Context, client *http.Client, p Provider, malID, pageNum int) (*page, error) {
	u := fmt.Sprintf("%s/anime/%d/episodes?page=%d", strings.TrimRight(p.BaseURL, "/"), malID, pageNum)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &providerError{Provider: p.Name, Kind: errUnavailable, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GarceslabAnime-EpisodeTags/1.0")

	res, err := client.Do(req)
	if err != nil {
		return nil, &providerError{Provider: p.Name, Kind: errUnavailable, Err: err}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxPageBytes))
	if err != nil {
		return nil, &providerError{Provider: p.Name, Kind: errUnavailable, Status: res.StatusCode, Err: err}
	}

	switch {
	case res.StatusCode == http.StatusTooManyRequests || isRateLimitBody(body):
		return nil, &providerError{Provider: p.Name, Kind: errRateLimited, Status: res.StatusCode, RetryAfter: parseRetryAfter(res.Header.Get("Retry-After"), time.Now())}
	case res.StatusCode == http.StatusNotFound:
		return nil, &providerError{Provider: p.Name, Kind: errNotFound, Status: res.StatusCode}
	case res.StatusCode != http.StatusOK:
		return nil, &providerError{Provider: p.Name, Kind: errUnavailable, Status: res.StatusCode}
	}

	var pg *page
	switch p.Format {
	case FormatJikanEdge:
		pg, err = parseEdge(body)
	default:
		pg, err = parseJikanV4(body)
	}
	if err != nil {
		return nil, &providerError{Provider: p.Name, Kind: errUnavailable, Status: res.StatusCode, Err: err}
	}
	return pg, nil
}

// isRateLimitBody catches a rate limit reported in the body with another status
// (jikan-edge: {"error":{"code":"RATE_LIMITED"}}; Jikan: {"type":"RateLimitException"}).
func isRateLimitBody(body []byte) bool {
	if len(body) == 0 || len(body) > 4096 {
		return false
	}
	var b struct {
		Type  string `json:"type"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &b) != nil {
		return false
	}
	return b.Type == "RateLimitException" || (b.Error != nil && b.Error.Code == "RATE_LIMITED")
}

// parseRetryAfter reads seconds or an HTTP date; 0 when absent or invalid.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil {
		if s < 0 {
			return 0
		}
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

func parseJikanV4(body []byte) (*page, error) {
	var b struct {
		Pagination *struct {
			HasNextPage bool `json:"has_next_page"`
		} `json:"pagination"`
		Data *[]struct {
			MalID  int  `json:"mal_id"`
			Filler bool `json:"filler"`
			Recap  bool `json:"recap"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, err
	}
	if b.Data == nil {
		return nil, errors.New("no data in the response")
	}
	pg := &page{HasNext: b.Pagination != nil && b.Pagination.HasNextPage}
	for _, e := range *b.Data {
		if e.MalID > 0 {
			pg.Episodes = append(pg.Episodes, sourceEpisode{Number: e.MalID, Filler: e.Filler, Recap: e.Recap})
		}
	}
	return pg, nil
}

func parseEdge(body []byte) (*page, error) {
	var b struct {
		Data *[]struct {
			Number int  `json:"number"`
			Filler bool `json:"filler"`
			Recap  bool `json:"recap"`
		} `json:"data"`
		Meta *struct {
			Pagination *struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pagination"`
		} `json:"meta"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, err
	}
	if b.Error != nil {
		return nil, fmt.Errorf("%s: %s", b.Error.Code, b.Error.Message)
	}
	if b.Data == nil {
		return nil, errors.New("no data in the response")
	}
	pg := &page{HasNext: b.Meta != nil && b.Meta.Pagination != nil && b.Meta.Pagination.HasNextPage}
	for _, e := range *b.Data {
		if e.Number > 0 {
			pg.Episodes = append(pg.Episodes, sourceEpisode{Number: e.Number, Filler: e.Filler, Recap: e.Recap})
		}
	}
	return pg, nil
}
