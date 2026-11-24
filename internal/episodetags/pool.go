package episodetags

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	// defaultBaseCooldown is how long a provider rests after its first failure;
	// it doubles with each further failure in a row, up to defaultMaxCooldown.
	defaultBaseCooldown = 30 * time.Second
	defaultMaxCooldown  = 30 * time.Minute
	// defaultMaxWait is how long a fetch waits for a provider to come off its
	// cooldown when every provider is resting. Past that the fetch gives up.
	defaultMaxWait = 2 * time.Minute
	// maxPages caps a series at 5000 episodes (One Piece has ~12 pages).
	maxPages = 50
)

// errAllProvidersDown means every provider failed or is resting for longer than maxWait.
var errAllProvidersDown = errors.New("episodetags: every provider is unavailable")

type providerState struct {
	Provider
	// nextAt is the earliest time of the next request (MinInterval pacing).
	nextAt time.Time
	// cooldownUntil keeps a failing provider out of rotation until then.
	cooldownUntil time.Time
	failures      int
}

// pool sends requests one at a time, to the first provider in order that isn't
// resting, and moves on to the next one when a provider is rate limited (429),
// erroring (5xx, bad body) or unreachable. Callers never see a rate limit: a
// page either comes back from some provider or the fetch fails as a whole.
type pool struct {
	mu        sync.Mutex // guards the states; requests themselves are serialized by the caller
	providers []*providerState
	client    *http.Client
	logger    *zerolog.Logger

	baseCooldown time.Duration
	maxCooldown  time.Duration
	maxWait      time.Duration
	now          func() time.Time
	sleep        func(ctx context.Context, d time.Duration) error
}

func newPool(providers []Provider, client *http.Client, logger *zerolog.Logger) *pool {
	if logger == nil {
		nop := zerolog.Nop()
		logger = &nop
	}
	p := &pool{
		client:       client,
		logger:       logger,
		baseCooldown: defaultBaseCooldown,
		maxCooldown:  defaultMaxCooldown,
		maxWait:      defaultMaxWait,
		now:          time.Now,
		sleep:        sleepCtx,
	}
	for _, pr := range providers {
		if pr.MinInterval < time.Second {
			// Never more than one request per second to the same provider.
			pr.MinInterval = time.Second
		}
		p.providers = append(p.providers, &providerState{Provider: pr})
	}
	return p
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// pick returns the first provider that isn't resting, or how long until one is.
func (p *pool) pick(now time.Time) (*providerState, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var wait time.Duration = -1
	for _, s := range p.providers {
		if !now.Before(s.cooldownUntil) {
			return s, 0
		}
		if d := s.cooldownUntil.Sub(now); wait < 0 || d < wait {
			wait = d
		}
	}
	return nil, wait
}

// cooldownFor is base * 2^(failures-1), capped, and at least retryAfter.
func (p *pool) cooldownFor(failures int, retryAfter time.Duration) time.Duration {
	d := p.baseCooldown
	for i := 1; i < failures && d < p.maxCooldown; i++ {
		d *= 2
	}
	if d > p.maxCooldown {
		d = p.maxCooldown
	}
	if retryAfter > d {
		// Honour Retry-After even past the cap, within reason.
		d = min(retryAfter, 6*time.Hour)
	}
	return d
}

// fetchPage gets one page from whichever provider can serve it. Each provider
// is tried at most twice per page; a 404 is an answer (the anime isn't on MAL)
// and is returned as is.
func (p *pool) fetchPage(ctx context.Context, malID, pageNum int) (*page, string, error) {
	if len(p.providers) == 0 {
		return nil, "", errAllProvidersDown
	}
	var lastErr error
	for attempt := 0; attempt < 2*len(p.providers); attempt++ {
		s, wait := p.pick(p.now())
		if s == nil {
			if wait > p.maxWait {
				break
			}
			if err := p.sleep(ctx, wait); err != nil {
				return nil, "", err
			}
			if s, _ = p.pick(p.now()); s == nil {
				continue
			}
		}

		p.mu.Lock()
		delay := s.nextAt.Sub(p.now())
		p.mu.Unlock()
		if err := p.sleep(ctx, delay); err != nil {
			return nil, "", err
		}

		pg, err := fetchPage(ctx, p.client, s.Provider, malID, pageNum)

		p.mu.Lock()
		now := p.now()
		s.nextAt = now.Add(s.MinInterval)
		if err == nil || isNotFound(err) {
			s.failures = 0
			p.mu.Unlock()
			return pg, s.Name, err
		}
		if ctx.Err() != nil {
			p.mu.Unlock()
			return nil, "", ctx.Err()
		}
		s.failures++
		var retryAfter time.Duration
		var pe *providerError
		if errors.As(err, &pe) {
			retryAfter = pe.RetryAfter
		}
		cd := p.cooldownFor(s.failures, retryAfter)
		s.cooldownUntil = now.Add(cd)
		p.mu.Unlock()

		p.logger.Debug().Err(err).Str("provider", s.Name).Dur("cooldown", cd).Int("malId", malID).Int("page", pageNum).
			Msg("episodetags: Provider failed, trying the next one")
		lastErr = err
	}
	if lastErr != nil {
		return nil, "", fmt.Errorf("%w (last: %v)", errAllProvidersDown, lastErr)
	}
	return nil, "", errAllProvidersDown
}

// fetchAll gets every page of a MAL anime and classifies its episodes. Pages may
// come from different providers (they serve the same MAL data, 100 per page);
// the source is the provider of the first page.
func (p *pool) fetchAll(ctx context.Context, malID int) (*Entry, error) {
	entry := &Entry{MalID: malID, Episodes: map[int]Kind{}}
	for n := 1; n <= maxPages; n++ {
		pg, source, err := p.fetchPage(ctx, malID, n)
		if err != nil {
			if isNotFound(err) {
				if n > 1 {
					break // past the last page
				}
				entry.NotFound = true
				entry.Source = source
				entry.FetchedAt = p.now()
				return entry, nil
			}
			return nil, err
		}
		if n == 1 {
			entry.Source = source
		}
		for _, e := range pg.Episodes {
			entry.Episodes[e.Number] = kindOf(e.Filler, e.Recap)
		}
		if !pg.HasNext || len(pg.Episodes) == 0 {
			break
		}
	}
	entry.FetchedAt = p.now()
	return entry, nil
}

// ProviderStatus is a provider's rotation state (for logs and tests).
type ProviderStatus struct {
	Name          string
	CooldownUntil time.Time
	Failures      int
}

func (p *pool) status() []ProviderStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	ret := make([]ProviderStatus, 0, len(p.providers))
	for _, s := range p.providers {
		ret = append(ret, ProviderStatus{Name: s.Name, CooldownUntil: s.cooldownUntil, Failures: s.failures})
	}
	return ret
}
