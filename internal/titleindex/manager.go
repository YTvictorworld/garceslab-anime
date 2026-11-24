package titleindex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog"
)

const (
	// DefaultURL is the latest release of the zstd-compressed "minified" database
	// (about 6 MB; 60 MB once decompressed).
	DefaultURL = "https://github.com/manami-project/anime-offline-database/releases/latest/download/anime-offline-database-minified.json.zst"
	// FileName is the cached download inside the index directory.
	FileName = "anime-offline-database-minified.json.zst"
	// DefaultMaxAge is how old the cached file may get before a new release is
	// downloaded (the database is released weekly).
	DefaultMaxAge = 7 * 24 * time.Hour
	// DisableEnvVar turns the index off (no download, empty suggestions).
	DisableEnvVar = "SEANIME_TITLE_INDEX_DISABLED"

	checkInterval   = 6 * time.Hour
	maxDownloadSize = 64 << 20  // compressed
	maxDecodedSize  = 512 << 20 // decompressed
	// A release with fewer AniList entries than this is treated as broken.
	minEntries = 1000
)

// Options configures a Manager.
type Options struct {
	// Dir is where the database file is cached (created if missing).
	Dir    string
	Logger *zerolog.Logger
	// URL overrides DefaultURL.
	URL string
	// MaxAge overrides DefaultMaxAge.
	MaxAge time.Duration
	// IsOffline reports whether the server is in offline mode (no downloads then).
	IsOffline func() bool
	// HTTPClient overrides the default client (10 minute timeout).
	HTTPClient *http.Client
	// MinEntries overrides the sanity check on a downloaded release (tests).
	MinEntries int
}

// Manager keeps the title index in memory: it loads the cached database file,
// downloads a new release in the background when the file is missing or older
// than MaxAge, and serves searches from the last good index. Until an index is
// loaded (or if every download fails), searches return an empty list.
type Manager struct {
	opts    Options
	index   atomic.Pointer[Index]
	refresh sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewManager returns a manager; call Start to load and refresh the index.
func NewManager(opts Options) *Manager {
	if opts.URL == "" {
		opts.URL = DefaultURL
	}
	if opts.MaxAge <= 0 {
		opts.MaxAge = DefaultMaxAge
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Minute}
	}
	if opts.MinEntries <= 0 {
		opts.MinEntries = minEntries
	}
	if opts.Logger == nil {
		nop := zerolog.Nop()
		opts.Logger = &nop
	}
	return &Manager{opts: opts}
}

// Disabled reports whether the index was turned off with DisableEnvVar.
func Disabled() bool {
	v := os.Getenv(DisableEnvVar)
	return v == "1" || v == "true" || v == "TRUE" || v == "True"
}

func (m *Manager) path() string { return filepath.Join(m.opts.Dir, FileName) }

// Search returns suggestions from the current index (empty when there is none).
func (m *Manager) Search(query string, limit int) []Suggestion {
	if m == nil {
		return []Suggestion{}
	}
	return m.index.Load().Search(query, limit)
}

// Index returns the current index (nil until one is loaded).
func (m *Manager) Index() *Index {
	if m == nil {
		return nil
	}
	return m.index.Load()
}

// Start loads the cached file and keeps the index fresh until Stop. It returns
// immediately; the work runs in a goroutine.
func (m *Manager) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		if err := m.loadFromDisk(); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.opts.Logger.Warn().Err(err).Msg("titleindex: Could not load the cached title index")
		}
		m.refreshIfStale(ctx)
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.refreshIfStale(ctx)
			}
		}
	}()
}

// Stop ends the background refresh (an ongoing download is cancelled).
func (m *Manager) Stop() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	<-m.done
}

func (m *Manager) loadFromDisk() error {
	f, err := os.Open(m.path())
	if err != nil {
		return err
	}
	defer f.Close()
	ix, err := parseCompressed(f)
	if err != nil {
		return err
	}
	m.index.Store(ix)
	m.opts.Logger.Info().Int("anime", ix.Len()).Str("lastUpdate", ix.LastUpdate()).Msg("titleindex: Loaded the title index")
	return nil
}

func (m *Manager) refreshIfStale(ctx context.Context) {
	if m.opts.IsOffline != nil && m.opts.IsOffline() {
		return
	}
	if info, err := os.Stat(m.path()); err == nil && time.Since(info.ModTime()) < m.opts.MaxAge && m.index.Load() != nil {
		return
	}
	if err := m.Refresh(ctx); err != nil && ctx.Err() == nil {
		m.opts.Logger.Warn().Err(err).Msg("titleindex: Could not download the anime title database; suggestions stay on the previous index (or empty)")
	}
}

// Refresh downloads the database, checks that it parses, then replaces the
// cached file and the in-memory index. On failure both stay as they were.
func (m *Manager) Refresh(ctx context.Context) error {
	m.refresh.Lock()
	defer m.refresh.Unlock()

	if err := os.MkdirAll(m.opts.Dir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.opts.URL, nil)
	if err != nil {
		return err
	}
	res, err := m.opts.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("titleindex: download: HTTP %d", res.StatusCode)
	}

	tmp, err := os.CreateTemp(m.opts.Dir, FileName+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after the rename

	n, err := io.Copy(tmp, io.LimitReader(res.Body, maxDownloadSize+1))
	if err == nil && n > maxDownloadSize {
		err = errors.New("titleindex: download is too large")
	}
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	var ix *Index
	if err == nil {
		ix, err = parseCompressed(tmp)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if ix.Len() < m.opts.MinEntries {
		return fmt.Errorf("titleindex: download has only %d AniList entries", ix.Len())
	}
	if err := os.Rename(tmpName, m.path()); err != nil {
		return err
	}
	m.index.Store(ix)
	m.opts.Logger.Info().Int("anime", ix.Len()).Str("lastUpdate", ix.LastUpdate()).Int64("bytes", n).Msg("titleindex: Downloaded the anime title database")
	return nil
}

func parseCompressed(r io.Reader) (*Index, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return Parse(io.LimitReader(zr, maxDecodedSize))
}
