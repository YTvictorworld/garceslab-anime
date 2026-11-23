package continuity

import (
	"context"
	"fmt"
	"seanime/internal/database/db_bridge"
	"seanime/internal/hook"
	"seanime/internal/library/anime"
	"seanime/internal/util"
	"strings"
	"time"
)

const (
	MaxWatchHistoryItems   = 100
	IgnoreRatioThreshold   = 0.9
	WatchHistoryBucketName = "watch_history"
)

type (
	// WatchHistory is a map of WatchHistoryItem.
	// The key is the WatchHistoryItem.MediaId.
	WatchHistory map[int]*WatchHistoryItem

	// WatchHistoryItem are stored in the file cache.
	// The history is used to resume playback from the last known position.
	// Item.MediaId and Item.EpisodeNumber are used to identify the media and episode.
	// Only one Item per MediaId should exist in the history.
	WatchHistoryItem struct {
		Kind Kind `json:"kind"`
		// Used for MediastreamKind and ExternalPlayerKind.
		Filepath      string `json:"filepath"`
		MediaId       int    `json:"mediaId"`
		EpisodeNumber int    `json:"episodeNumber"`
		// The current playback time in seconds.
		// Used to determine when to remove the item from the history.
		CurrentTime float64 `json:"currentTime"`
		// The duration of the media in seconds.
		Duration float64 `json:"duration"`
		// Timestamp of when the item was added to the history.
		TimeAdded time.Time `json:"timeAdded"`
		// TimeAdded is used in conjunction with TimeUpdated
		// Timestamp of when the item was last updated.
		// Used to determine when to remove the item from the history (First in, first out).
		TimeUpdated time.Time `json:"timeUpdated"`
	}

	WatchHistoryItemResponse struct {
		Item  *WatchHistoryItem `json:"item"`
		Found bool              `json:"found"`
	}

	UpdateWatchHistoryItemOptions struct {
		CurrentTime   float64 `json:"currentTime"`
		Duration      float64 `json:"duration"`
		MediaId       int     `json:"mediaId"`
		EpisodeNumber int     `json:"episodeNumber"`
		Filepath      string  `json:"filepath,omitempty"`
		Kind          Kind    `json:"kind"`
	}
)

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

// profiles-hook: the *Ctx variants carry the profile; the others use the
// background context (the profile that owns the playback).

func (m *Manager) GetWatchHistory() WatchHistory {
	return m.GetWatchHistoryCtx(m.background())
}

func (m *Manager) GetWatchHistoryCtx(ctx context.Context) WatchHistory {
	defer util.HandlePanicInModuleThen("continuity/GetWatchHistory", func() {})

	m.mu.RLock()
	defer m.mu.RUnlock()

	items, err := m.store.All(ctx)
	if err != nil {
		m.logger.Error().Err(err).Msg("continuity: Failed to get watch history")
		return nil
	}

	ret := make(WatchHistory)
	for _, item := range items {
		ret[item.MediaId] = item
	}

	return ret
}

func (m *Manager) GetWatchHistoryItem(mediaId int) *WatchHistoryItemResponse {
	return m.GetWatchHistoryItemCtx(m.background(), mediaId)
}

func (m *Manager) GetWatchHistoryItemCtx(ctx context.Context, mediaId int) *WatchHistoryItemResponse {
	defer util.HandlePanicInModuleThen("continuity/GetWatchHistoryItem", func() {})

	m.mu.RLock()
	defer m.mu.RUnlock()

	i, found := m.getWatchHistory(ctx, mediaId)
	return &WatchHistoryItemResponse{
		Item:  i,
		Found: found,
	}
}

// UpdateWatchHistoryItem updates the WatchHistoryItem in the file cache.
func (m *Manager) UpdateWatchHistoryItem(opts *UpdateWatchHistoryItemOptions) (err error) {
	return m.UpdateWatchHistoryItemCtx(m.background(), opts)
}

func (m *Manager) UpdateWatchHistoryItemCtx(ctx context.Context, opts *UpdateWatchHistoryItemOptions) (err error) {
	defer util.HandlePanicInModuleWithError("continuity/UpdateWatchHistoryItem", &err)

	m.mu.Lock()
	defer m.mu.Unlock()

	added := false

	// Get the current history
	i, found := m.getWatchHistory(ctx, opts.MediaId)
	if !found {
		added = true
		i = &WatchHistoryItem{
			Kind:          opts.Kind,
			Filepath:      opts.Filepath,
			MediaId:       opts.MediaId,
			EpisodeNumber: opts.EpisodeNumber,
			CurrentTime:   opts.CurrentTime,
			Duration:      opts.Duration,
			TimeAdded:     time.Now(),
			TimeUpdated:   time.Now(),
		}
	} else {
		i.Kind = opts.Kind
		i.EpisodeNumber = opts.EpisodeNumber
		i.CurrentTime = opts.CurrentTime
		i.Duration = opts.Duration
		i.TimeUpdated = time.Now()
	}

	// Save the i
	err = m.store.Set(ctx, i)
	if err != nil {
		return fmt.Errorf("continuity: Failed to save watch history item: %w", err)
	}

	_ = hook.GlobalHookManager.OnWatchHistoryItemUpdated().Trigger(&WatchHistoryItemUpdatedEvent{
		WatchHistoryItem: i,
	})

	// If the item was added, check if we need to remove the oldest item
	if added {
		_ = m.trimWatchHistoryItemsCtx(ctx)
	}

	return nil
}

func (m *Manager) DeleteWatchHistoryItem(mediaId int) (err error) {
	return m.DeleteWatchHistoryItemCtx(m.background(), mediaId)
}

func (m *Manager) DeleteWatchHistoryItemCtx(ctx context.Context, mediaId int) (err error) {
	defer util.HandlePanicInModuleWithError("continuity/DeleteWatchHistoryItem", &err)

	m.mu.Lock()
	defer m.mu.Unlock()

	err = m.store.Delete(ctx, mediaId)
	if err != nil {
		return fmt.Errorf("continuity: Failed to delete watch history item: %w", err)
	}

	return nil
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

// GetExternalPlayerEpisodeWatchHistoryItem is called before launching the external player to get the last known position.
// Unlike GetWatchHistoryItem, this checks if the episode numbers match.
func (m *Manager) GetExternalPlayerEpisodeWatchHistoryItem(path string, isStream bool, episode, mediaId int) (ret *WatchHistoryItemResponse) {
	defer util.HandlePanicInModuleThen("continuity/GetExternalPlayerEpisodeWatchHistoryItem", func() {})

	m.mu.RLock()
	defer m.mu.RUnlock()

	ctx := m.background() // profiles-hook
	if !m.settingsLocked(ctx).WatchContinuityEnabled {
		return &WatchHistoryItemResponse{
			Item:  nil,
			Found: false,
		}
	}

	ret = &WatchHistoryItemResponse{
		Item:  nil,
		Found: false,
	}

	m.logger.Debug().
		Str("path", path).
		Bool("isStream", isStream).
		Int("episode", episode).
		Int("mediaId", mediaId).
		Msg("continuity: Retrieving watch history item")

	// Normalize path
	path = util.NormalizePath(path)

	if isStream {

		event := &WatchHistoryStreamEpisodeItemRequestedEvent{
			WatchHistoryItem: &WatchHistoryItem{},
		}

		hook.GlobalHookManager.OnWatchHistoryStreamEpisodeItemRequested().Trigger(event)
		if event.DefaultPrevented {
			return &WatchHistoryItemResponse{
				Item:  event.WatchHistoryItem,
				Found: event.WatchHistoryItem != nil,
			}
		}

		if episode == 0 || mediaId == 0 {
			m.logger.Debug().
				Int("episode", episode).
				Int("mediaId", mediaId).
				Msg("continuity: No episode or media provided")
			return
		}

		i, found := m.getWatchHistory(ctx, mediaId)
		if !found || i.EpisodeNumber != episode {
			m.logger.Trace().
				Interface("item", i).
				Msg("continuity: No watch history item found or episode number does not match")
			return
		}

		m.logger.Debug().
			Interface("item", i).
			Msg("continuity: Watch history item found")

		return &WatchHistoryItemResponse{
			Item:  i,
			Found: found,
		}

	} else {
		// Find the local file from the path
		lfs, _, err := db_bridge.GetLocalFiles(m.db)
		if err != nil {
			return ret
		}

		event := &WatchHistoryLocalFileEpisodeItemRequestedEvent{
			Path:             path,
			LocalFiles:       lfs,
			WatchHistoryItem: &WatchHistoryItem{},
		}
		hook.GlobalHookManager.OnWatchHistoryLocalFileEpisodeItemRequested().Trigger(event)
		if event.DefaultPrevented {
			return &WatchHistoryItemResponse{
				Item:  event.WatchHistoryItem,
				Found: event.WatchHistoryItem != nil,
			}
		}

		var lf *anime.LocalFile
		// Find the local file from the path
		for _, l := range lfs {
			if l.GetNormalizedPath() == path {
				lf = l
				m.logger.Trace().Msg("continuity: Local file found from path")
				break
			}
		}
		// If the local file is not found, the path might be a filename (in the case of VLC)
		if lf == nil {
			for _, l := range lfs {
				if strings.ToLower(l.Name) == path {
					lf = l
					m.logger.Trace().Msg("continuity: Local file found from filename")
					break
				}
			}
		}

		if lf == nil || lf.MediaId == 0 || !lf.IsMain() {
			m.logger.Trace().Msg("continuity: Local file not found or not main")
			return
		}

		i, found := m.getWatchHistory(ctx, lf.MediaId)
		if !found || i.EpisodeNumber != lf.GetEpisodeNumber() {
			m.logger.Trace().
				Interface("item", i).
				Msg("continuity: No watch history item found or episode number does not match")
			return
		}

		m.logger.Debug().
			Interface("item", i).
			Msg("continuity: Watch history item found")

		return &WatchHistoryItemResponse{
			Item:  i,
			Found: found,
		}
	}
}

func (m *Manager) UpdateExternalPlayerEpisodeWatchHistoryItem(currentTime, duration float64) {
	defer util.HandlePanicInModuleThen("continuity/UpdateWatchHistoryItem", func() {})

	m.mu.Lock()
	defer m.mu.Unlock()

	ctx := m.background() // profiles-hook
	if !m.settingsLocked(ctx).WatchContinuityEnabled {
		return
	}

	if m.externalPlayerEpisodeDetails.IsAbsent() {
		return
	}

	added := false

	opts, ok := m.externalPlayerEpisodeDetails.Get()
	if !ok {
		return
	}

	// Get the current history
	i, found := m.getWatchHistory(ctx, opts.MediaId)
	if !found {
		added = true
		i = &WatchHistoryItem{
			Kind:          ExternalPlayerKind,
			Filepath:      opts.Filepath,
			MediaId:       opts.MediaId,
			EpisodeNumber: opts.EpisodeNumber,
			CurrentTime:   currentTime,
			Duration:      duration,
			TimeAdded:     time.Now(),
			TimeUpdated:   time.Now(),
		}
	} else {
		i.Kind = ExternalPlayerKind
		i.EpisodeNumber = opts.EpisodeNumber
		i.CurrentTime = currentTime
		i.Duration = duration
		i.TimeUpdated = time.Now()
	}

	// Save the i
	_ = m.store.Set(ctx, i)

	// If the item was added, check if we need to remove the oldest item
	if added {
		_ = m.trimWatchHistoryItemsCtx(ctx)
	}

	return
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

func (m *Manager) getWatchHistory(ctx context.Context, mediaId int) (ret *WatchHistoryItem, exists bool) {
	defer util.HandlePanicInModuleThen("continuity/getWatchHistory", func() {
		ret = nil
		exists = false
	})

	reqEvent := &WatchHistoryItemRequestedEvent{
		MediaId:          mediaId,
		WatchHistoryItem: ret,
	}
	hook.GlobalHookManager.OnWatchHistoryItemRequested().Trigger(reqEvent)
	ret = reqEvent.WatchHistoryItem

	if reqEvent.DefaultPrevented {
		return reqEvent.WatchHistoryItem, reqEvent.WatchHistoryItem != nil
	}

	ret, exists, _ = m.store.Get(ctx, mediaId)

	if exists && ret != nil && ret.Duration > 0 {
		// If the item completion ratio is equal or above IgnoreRatioThreshold, don't return anything
		ratio := ret.CurrentTime / ret.Duration
		if ratio >= IgnoreRatioThreshold {
			// keep the timestamp for last-watched sorting
			return nil, false
		}
		if ratio < 0.05 {
			return nil, false
		}
	}

	return
}

// removes the oldest WatchHistoryItem from the store.
func (m *Manager) trimWatchHistoryItems() error {
	return m.trimWatchHistoryItemsCtx(m.background())
}

func (m *Manager) trimWatchHistoryItemsCtx(ctx context.Context) error {
	defer util.HandlePanicInModuleThen("continuity/TrimWatchHistoryItems", func() {})

	if err := m.store.Trim(ctx, MaxWatchHistoryItems); err != nil {
		return fmt.Errorf("continuity: Failed to remove oldest watch history item: %w", err)
	}

	return nil
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
