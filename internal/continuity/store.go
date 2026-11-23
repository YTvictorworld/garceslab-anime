package continuity

// Garceslab Anime (profiles-hook): pluggable watch history storage. Upstream keeps
// one global history in the file cache; with local profiles the history lives in
// profiles.db, per profile (see internal/profiles/HOOKS.md).

import (
	"context"
	"seanime/internal/util/filecache"
	"strconv"
)

// HistoryStore stores the watch history. The profile (if any) travels in ctx.
type HistoryStore interface {
	All(ctx context.Context) (map[int]*WatchHistoryItem, error)
	Get(ctx context.Context, mediaId int) (*WatchHistoryItem, bool, error)
	Set(ctx context.Context, item *WatchHistoryItem) error
	Delete(ctx context.Context, mediaId int) error
	// Trim removes the oldest item when there are more than max.
	Trim(ctx context.Context, max int) error
}

// fileCacheHistoryStore is the upstream storage (global file cache bucket).
type fileCacheHistoryStore struct {
	fileCacher *filecache.Cacher
	bucket     *filecache.Bucket
}

func (s *fileCacheHistoryStore) All(context.Context) (map[int]*WatchHistoryItem, error) {
	items, err := filecache.GetAll[*WatchHistoryItem](s.fileCacher, *s.bucket)
	if err != nil {
		return nil, err
	}
	ret := make(map[int]*WatchHistoryItem, len(items))
	for _, item := range items {
		ret[item.MediaId] = item
	}
	return ret, nil
}

func (s *fileCacheHistoryStore) Get(_ context.Context, mediaId int) (ret *WatchHistoryItem, found bool, err error) {
	found, err = s.fileCacher.Get(*s.bucket, strconv.Itoa(mediaId), &ret)
	return ret, found, err
}

func (s *fileCacheHistoryStore) Set(_ context.Context, item *WatchHistoryItem) error {
	return s.fileCacher.Set(*s.bucket, strconv.Itoa(item.MediaId), item)
}

func (s *fileCacheHistoryStore) Delete(_ context.Context, mediaId int) error {
	return s.fileCacher.Delete(*s.bucket, strconv.Itoa(mediaId))
}

func (s *fileCacheHistoryStore) Trim(_ context.Context, max int) error {
	items, err := filecache.GetAll[*WatchHistoryItem](s.fileCacher, *s.bucket)
	if err != nil {
		return err
	}
	if len(items) <= max {
		return nil
	}
	var oldestKey string
	for key := range items {
		if oldestKey == "" || items[key].TimeUpdated.Before(items[oldestKey].TimeUpdated) {
			oldestKey = key
		}
	}
	return s.fileCacher.Delete(*s.bucket, oldestKey)
}

// SetHistoryStore replaces the watch history storage. background returns the
// context used by the calls that have none (server-side players): with profiles,
// the context of the profile that owns the playback. settings, if set, returns the
// continuity settings for a context (per-profile "watch continuity" switch).
func (m *Manager) SetHistoryStore(store HistoryStore, background func() context.Context, settings func(ctx context.Context) *Settings) {
	if m == nil || store == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
	m.backgroundCtx = background
	m.settingsFor = settings
}

func (m *Manager) background() context.Context {
	if m.backgroundCtx != nil {
		return m.backgroundCtx()
	}
	return context.Background()
}

// GetSettingsCtx returns the continuity settings for the profile in ctx.
func (m *Manager) GetSettingsCtx(ctx context.Context) *Settings {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settingsLocked(ctx)
}

func (m *Manager) settingsLocked(ctx context.Context) *Settings {
	if m.settingsFor != nil {
		if s := m.settingsFor(ctx); s != nil {
			return s
		}
	}
	return m.settings
}
