package episodetags

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	// DefaultFinishedTTL is how long the tags of a finished anime stay fresh.
	DefaultFinishedTTL = 30 * 24 * time.Hour
	// DefaultAiringTTL is how long the tags of an airing anime stay fresh.
	DefaultAiringTTL = 24 * time.Hour
	// DefaultNotFoundTTL is how long a "not on MAL" answer is kept.
	DefaultNotFoundTTL = 7 * 24 * time.Hour
	// DefaultRetryAfterFailure is how long an anime whose fetch failed (every
	// provider down) answers "unavailable" before a new fetch is tried.
	DefaultRetryAfterFailure = 15 * time.Minute

	queueSize = 256
)

// Options configures a Manager.
type Options struct {
	// Dir holds the cache, one JSON file per MAL anime (created if missing).
	Dir    string
	Logger *zerolog.Logger
	// Providers in order of preference (DefaultProviders when empty).
	Providers []Provider
	// IsOffline reports whether the server is in offline mode (no fetches then).
	IsOffline  func() bool
	HTTPClient *http.Client

	FinishedTTL       time.Duration
	AiringTTL         time.Duration
	NotFoundTTL       time.Duration
	RetryAfterFailure time.Duration
}

// Manager serves episode tags from the disk cache and fetches missing or stale
// ones in the background, one anime at a time, through the provider pool.
type Manager struct {
	opts Options
	pool *pool
	now  func() time.Time

	mu       sync.Mutex
	entries  map[int]*Entry // loaded from disk on first use
	queued   map[int]bool   // queued or being fetched
	failedAt map[int]time.Time

	queue  chan int
	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager returns a manager; call Start to run the background worker.
func NewManager(opts Options) *Manager {
	if opts.Logger == nil {
		nop := zerolog.Nop()
		opts.Logger = &nop
	}
	if len(opts.Providers) == 0 {
		opts.Providers = DefaultProviders()
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = newHTTPClient()
	}
	if opts.FinishedTTL <= 0 {
		opts.FinishedTTL = DefaultFinishedTTL
	}
	if opts.AiringTTL <= 0 {
		opts.AiringTTL = DefaultAiringTTL
	}
	if opts.NotFoundTTL <= 0 {
		opts.NotFoundTTL = DefaultNotFoundTTL
	}
	if opts.RetryAfterFailure <= 0 {
		opts.RetryAfterFailure = DefaultRetryAfterFailure
	}
	return &Manager{
		opts:     opts,
		pool:     newPool(opts.Providers, opts.HTTPClient, opts.Logger),
		now:      time.Now,
		entries:  map[int]*Entry{},
		queued:   map[int]bool{},
		failedAt: map[int]time.Time{},
		queue:    make(chan int, queueSize),
	}
}

// Start runs the fetch worker until Stop.
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		for {
			select {
			case <-ctx.Done():
				return
			case malID := <-m.queue:
				m.fetch(ctx, malID)
			}
		}
	}()
}

// Stop ends the worker (an ongoing fetch is cancelled).
func (m *Manager) Stop() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	<-m.done
}

// Get returns the tags of a MAL anime. It never blocks on the network: a
// missing or stale entry is queued for a background fetch, and the response
// carries what is cached ("pending" when there is nothing yet). airing picks the
// refresh interval (daily while airing, monthly once finished).
func (m *Manager) Get(malID int, airing bool) Response {
	if m == nil || malID <= 0 {
		return unavailable()
	}
	offline := m.opts.IsOffline != nil && m.opts.IsOffline()
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	entry := m.lookupLocked(malID)
	if entry != nil {
		if !offline && m.staleLocked(entry, airing, now) {
			m.enqueueLocked(malID, now)
		}
		return entry.response()
	}
	if offline {
		return unavailable()
	}
	if t, ok := m.failedAt[malID]; ok && now.Sub(t) < m.opts.RetryAfterFailure && !m.queued[malID] {
		ret := unavailable()
		ret.MalID = malID
		return ret
	}
	m.enqueueLocked(malID, now)
	return pending(malID)
}

func (m *Manager) staleLocked(e *Entry, airing bool, now time.Time) bool {
	ttl := m.opts.FinishedTTL
	switch {
	case e.NotFound:
		ttl = m.opts.NotFoundTTL
	case airing:
		ttl = m.opts.AiringTTL
	}
	return now.Sub(e.FetchedAt) >= ttl
}

func (m *Manager) enqueueLocked(malID int, now time.Time) {
	if m.queued[malID] {
		return
	}
	// A stale entry whose refresh just failed keeps serving the old data until
	// the retry delay is over.
	if t, ok := m.failedAt[malID]; ok && now.Sub(t) < m.opts.RetryAfterFailure {
		return
	}
	select {
	case m.queue <- malID:
		m.queued[malID] = true
	default:
		// Queue full: a later request queues it again.
	}
}

func (m *Manager) fetch(ctx context.Context, malID int) {
	entry, err := m.pool.fetchAll(ctx, malID)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.queued, malID)
	if err != nil {
		if ctx.Err() == nil {
			m.failedAt[malID] = m.now()
			m.opts.Logger.Warn().Err(err).Int("malId", malID).Msg("episodetags: Could not fetch episode tags")
		}
		return
	}
	delete(m.failedAt, malID)
	m.entries[malID] = entry
	if err := m.saveLocked(entry); err != nil {
		m.opts.Logger.Warn().Err(err).Int("malId", malID).Msg("episodetags: Could not save episode tags")
	}
	m.opts.Logger.Debug().Int("malId", malID).Int("episodes", len(entry.Episodes)).Str("source", entry.Source).Msg("episodetags: Fetched episode tags")
}

func (m *Manager) path(malID int) string {
	return filepath.Join(m.opts.Dir, strconv.Itoa(malID)+".json")
}

func (m *Manager) lookupLocked(malID int) *Entry {
	if e, ok := m.entries[malID]; ok {
		return e
	}
	if m.opts.Dir == "" {
		return nil
	}
	b, err := os.ReadFile(m.path(malID))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.opts.Logger.Warn().Err(err).Int("malId", malID).Msg("episodetags: Could not read cached episode tags")
		}
		return nil
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil || e.MalID != malID {
		return nil // a corrupt file is refetched and overwritten
	}
	if e.Episodes == nil {
		e.Episodes = map[int]Kind{}
	}
	m.entries[malID] = &e
	return &e
}

func (m *Manager) saveLocked(e *Entry) error {
	if m.opts.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(m.opts.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.opts.Dir, strconv.Itoa(e.MalID)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after the rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, m.path(e.MalID))
}

// Providers returns the rotation state of each provider.
func (m *Manager) Providers() []ProviderStatus {
	if m == nil {
		return nil
	}
	return m.pool.status()
}
