package debrid_client

import (
	"context"
	"errors"
	"fmt"
	"seanime/internal/database/db_bridge"
	"seanime/internal/debrid/debrid"
	"seanime/internal/directstream"
	"seanime/internal/events"
	hibiketorrent "seanime/internal/extension/hibike/torrent"
	"seanime/internal/hook"
	"seanime/internal/library/playbackmanager"
	"seanime/internal/torrents/autoselect"
	"seanime/internal/util"
	"strconv"
	"sync"
	"time"

	"github.com/samber/mo"
)

type (
	// StreamManager runs the debrid streams. Garceslab Anime (phase 2): a stream
	// is a streamSlot. Relayed streams (played through the server: "default"
	// media player, "nativeplayer" direct stream) keep upstream's single slot,
	// and starting one replaces the previous relayed stream. Direct streams
	// ("externalPlayerLink", "none", "noneAndAwait": the client gets the debrid
	// link and plays it itself) get one slot per client id, so several clients
	// resolve and watch at once; their status events only go to their client.
	// A relayed start never cancels another client's direct stream and vice versa.
	StreamManager struct {
		repository *Repository

		mu      sync.Mutex
		relayed *streamSlot
		direct  map[string]*streamSlot
	}

	// streamSlot is the state of one stream (upstream's former StreamManager fields).
	streamSlot struct {
		mu                    sync.Mutex // guards the fields below the ids
		clientId              string
		isDirect              bool
		mediaId               int
		episodeNumber         int
		mediaTitle            string
		currentTorrentItemId  string
		downloadCtxCancelFunc context.CancelFunc
		currentStreamUrl      string

		playbackSubscriberCtxCancelFunc context.CancelFunc
		stopped                         bool
	}

	// DirectStreamEvent is what the observer of direct streams hears.
	DirectStreamEvent string

	// DirectStreamObserver hears how the direct stream of a client went
	// (Garceslab Anime: the viewer sessions registry frees a slot on failure).
	DirectStreamObserver func(clientId string, info DirectStreamInfo, ev DirectStreamEvent)

	// DirectStreamInfo describes a direct stream for its observer (never the link).
	DirectStreamInfo struct {
		MediaId       int
		EpisodeNumber int
		MediaTitle    string
	}

	StreamPlaybackType string

	StreamStatus string

	StreamState struct {
		Status      StreamStatus `json:"status"`
		TorrentName string       `json:"torrentName"`
		Message     string       `json:"message"`
	}

	StartStreamOptions struct {
		MediaId           int
		EpisodeNumber     int                         // RELATIVE Episode number to identify the file
		AniDBEpisode      string                      // Anizip episode
		Torrent           *hibiketorrent.AnimeTorrent // Selected torrent
		FileId            string                      // File ID or index
		FileIndex         *int                        // Index of the file to stream (Manual selection)
		UserAgent         string
		ClientId          string
		PlaybackType      StreamPlaybackType
		AutoSelect        bool
		BatchEpisodeFiles *hibiketorrent.BatchEpisodeFiles
	}

	CancelStreamOptions struct {
		// Whether to remove the torrent from the debrid service
		RemoveTorrent bool `json:"removeTorrent"`
	}
)

const (
	StreamStatusDownloading StreamStatus = "downloading"
	StreamStatusReady       StreamStatus = "ready"
	StreamStatusFailed      StreamStatus = "failed"
	StreamStatusStarted     StreamStatus = "started"
)

func NewStreamManager(repository *Repository) *StreamManager {
	return &StreamManager{
		repository: repository,
		direct:     map[string]*streamSlot{},
	}
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

const (
	DirectStreamResolving DirectStreamEvent = "resolving" // the media is known (MediaTitle set)
	DirectStreamReady     DirectStreamEvent = "ready"
	DirectStreamFailed    DirectStreamEvent = "failed"
)

// IsDirect reports whether the client plays the debrid link itself (Garceslab
// Anime): the server only resolves the link and never relays the video.
func (t StreamPlaybackType) IsDirect() bool {
	switch t {
	case PlaybackTypeExternalPlayer, PlaybackTypeNone, PlaybackTypeNoneAndAwait:
		return true
	}
	return false
}

const (
	PlaybackTypeNone           StreamPlaybackType = "none"
	PlaybackTypeNoneAndAwait   StreamPlaybackType = "noneAndAwait"
	PlaybackTypeDefault        StreamPlaybackType = "default"
	PlaybackTypeNativePlayer   StreamPlaybackType = "nativeplayer"
	PlaybackTypeExternalPlayer StreamPlaybackType = "externalPlayerLink"
)

// startStream is called by the client to start streaming a torrent
func (s *StreamManager) startStream(ctx context.Context, opts *StartStreamOptions) (err error) {
	defer util.HandlePanicInModuleWithError("debrid/client/StartStream", &err)

	s.mu.Lock()
	s.repository.previousStreamOptions = mo.Some(opts)
	s.mu.Unlock()

	s.repository.logger.Info().
		Str("clientId", opts.ClientId).
		Any("playbackType", opts.PlaybackType).
		Int("mediaId", opts.MediaId).Msgf("debridstream: Starting stream for episode %s", opts.AniDBEpisode)

	provider, err := s.repository.GetProvider()
	if err != nil {
		return fmt.Errorf("debridstream: Failed to start stream: %w", err)
	}

	// Garceslab Anime: take the slot of this stream (cancels only the streams it replaces).
	slot := s.takeSlot(opts)
	ws := s.eventsFor(slot)
	if slot.isDirect {
		ctx = autoselect.WithStatusClient(ctx, slot.clientId)
	}
	defer func() {
		if err != nil {
			s.notifyDirect(slot, DirectStreamFailed)
		}
	}()

	ws.SendEvent(events.ShowIndefiniteLoader, "debridstream")
	//defer func() {
	//	ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
	//}()

	if opts.PlaybackType == PlaybackTypeNativePlayer {
		s.repository.directStreamManager.BeginOpen(opts.ClientId, "Selecting torrent...", func() {
			s.repository.CancelStream(&CancelStreamOptions{RemoveTorrent: true})
		})
	}

	//
	// Get the media info
	//
	media, _, err := s.getMediaInfo(ctx, opts.MediaId)
	if err != nil {
		ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
		return err
	}
	if opts.PlaybackType == PlaybackTypeNativePlayer && !s.repository.directStreamManager.IsOpenActive(opts.ClientId) {
		ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
		return nil
	}
	if slot.isDirect {
		slot.mu.Lock()
		slot.mediaTitle = media.GetPreferredTitle()
		slot.mu.Unlock()
		s.notifyDirect(slot, DirectStreamResolving)
	}

	episodeNumber := opts.EpisodeNumber
	aniDbEpisode := strconv.Itoa(episodeNumber)

	selectedTorrent := opts.Torrent
	fileId := opts.FileId
	filepath := ""

	if opts.AutoSelect {

		ws.SendEvent(events.DebridStreamState, StreamState{
			Status:      StreamStatusDownloading,
			TorrentName: "-",
			Message:     "Selecting best torrent...",
		})

		pt, err := s.repository.findBestTorrent(ctx, provider, media, opts.EpisodeNumber)
		if err != nil {
			if opts.PlaybackType == PlaybackTypeNativePlayer {
				s.repository.directStreamManager.AbortOpen(opts.ClientId, err)
			}
			ws.SendEvent(events.DebridStreamState, StreamState{
				Status:      StreamStatusFailed,
				TorrentName: "-",
				Message:     fmt.Sprintf("Failed to select best torrent, %v", err),
			})
			ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
			return fmt.Errorf("debridstream: Failed to start stream: %w", err)
		}
		selectedTorrent = pt.torrent
		fileId = pt.fileId
		filepath = pt.filepath
	} else {
		// Manual selection
		if selectedTorrent == nil {
			ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
			return fmt.Errorf("debridstream: Failed to start stream, no torrent provided")
		}

		ws.SendEvent(events.DebridStreamState, StreamState{
			Status:      StreamStatusDownloading,
			TorrentName: selectedTorrent.Name,
			Message:     "Analyzing selected torrent...",
		})

		// If no fileId is provided, we need to analyze the torrent to find the correct file
		if fileId == "" {
			var chosenFileIndex *int
			if opts.FileIndex != nil {
				chosenFileIndex = opts.FileIndex
			}
			pt, err := s.repository.findBestTorrentFromManualSelection(provider, selectedTorrent, media, opts.EpisodeNumber, chosenFileIndex)
			if err != nil {
				if opts.PlaybackType == PlaybackTypeNativePlayer {
					s.repository.directStreamManager.AbortOpen(opts.ClientId, err)
				}
				ws.SendEvent(events.DebridStreamState, StreamState{
					Status:      StreamStatusFailed,
					TorrentName: selectedTorrent.Name,
					Message:     fmt.Sprintf("Failed to analyze torrent, %v", err),
				})
				ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
				return fmt.Errorf("debridstream: Failed to analyze torrent: %w", err)
			}
			selectedTorrent = pt.torrent
			fileId = pt.fileId
			filepath = pt.filepath
		}
	}

	if selectedTorrent == nil {
		if opts.PlaybackType == PlaybackTypeNativePlayer {
			s.repository.directStreamManager.AbortOpen(opts.ClientId, fmt.Errorf("debridstream: Failed to start stream, no torrent provided"))
		}
		ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
		return fmt.Errorf("debridstream: Failed to start stream, no torrent provided")
	}
	if opts.PlaybackType == PlaybackTypeNativePlayer && !s.repository.directStreamManager.IsOpenActive(opts.ClientId) {
		ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
		return nil
	}

	ws.SendEvent(events.DebridStreamState, StreamState{
		Status:      StreamStatusDownloading,
		TorrentName: selectedTorrent.Name,
		Message:     "Adding torrent...",
	})

	// Add the torrent to the debrid service
	torrentItemId, err := provider.AddTorrent(debrid.AddTorrentOptions{
		MagnetLink:   selectedTorrent.MagnetLink,
		InfoHash:     selectedTorrent.InfoHash,
		SelectFileId: fileId, // RD-only, download only the selected file
	})
	if err != nil {
		ws.SendEvent(events.DebridStreamState, StreamState{
			Status:      StreamStatusFailed,
			TorrentName: selectedTorrent.Name,
			Message:     fmt.Sprintf("Failed to add torrent, %v", err),
		})
		ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
		return fmt.Errorf("debridstream: Failed to add torrent: %w", err)
	}

	time.Sleep(150 * time.Millisecond)

	// Save the current torrent item id
	ctx, cancelCtx := context.WithCancel(context.Background())
	if !slot.begin(torrentItemId, cancelCtx) {
		// Replaced or cancelled while the torrent was being added
		cancelCtx()
		ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
		return nil
	}

	readyCh := make(chan struct{})
	readyOnce := sync.Once{}
	ready := func() {
		readyOnce.Do(func() {
			close(readyCh)
		})
	}

	// Launch a goroutine that will listen to the added torrent's status
	go func(ctx context.Context) {
		defer util.HandlePanicInModuleThen("debrid/client/StartStream", func() {})
		defer func() {
			ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
		}()

		defer func() {
			// Cancel the context
			slot.cancelDownload()
		}()

		s.repository.logger.Debug().Msg("debridstream: Listening to torrent status")

		ws.SendEvent(events.DebridStreamState, StreamState{
			Status:      StreamStatusDownloading,
			TorrentName: selectedTorrent.Name,
			Message:     fmt.Sprintf("Downloading torrent..."),
		})

		itemCh := make(chan debrid.TorrentItem, 1)

		go func() {
			for item := range itemCh {
				if opts.PlaybackType == PlaybackTypeNativePlayer {
					if !s.repository.directStreamManager.UpdateOpenStep(opts.ClientId, fmt.Sprintf("Awaiting stream: %d%%", item.CompletionPercentage)) {
						return
					}
				}

				ws.SendEvent(events.DebridStreamState, StreamState{
					Status:      StreamStatusDownloading,
					TorrentName: item.Name,
					Message:     fmt.Sprintf("Downloading torrent: %d%%", item.CompletionPercentage),
				})
			}
		}()

		// Await the stream URL
		// For Torbox, this will wait until the entire torrent is downloaded
		streamUrl, err := provider.GetTorrentStreamUrl(ctx, debrid.StreamTorrentOptions{
			ID:     torrentItemId,
			FileId: fileId,
		}, itemCh)

		go func() {
			close(itemCh)
		}()

		if ctx.Err() != nil {
			s.repository.logger.Debug().Msg("debridstream: Context cancelled, stopping stream")
			ready()
			return
		}
		if opts.PlaybackType == PlaybackTypeNativePlayer && !s.repository.directStreamManager.IsOpenActive(opts.ClientId) {
			ready()
			return
		}

		if err != nil {
			s.repository.logger.Err(err).Msg("debridstream: Failed to get stream URL")
			if !errors.Is(err, context.Canceled) {
				s.notifyDirect(slot, DirectStreamFailed)
				ws.SendEvent(events.DebridStreamState, StreamState{
					Status:      StreamStatusFailed,
					TorrentName: selectedTorrent.Name,
					Message:     fmt.Sprintf("Failed to get stream URL, %v", err),
				})
			}
			ready()
			return
		}

		skipCheckEvent := &DebridSkipStreamCheckEvent{
			StreamURL:  streamUrl,
			Retries:    4,
			RetryDelay: 8,
		}
		_ = hook.GlobalHookManager.OnDebridSkipStreamCheck().Trigger(skipCheckEvent)
		streamUrl = skipCheckEvent.StreamURL

		// Default prevented, we check if we can stream the file
		if skipCheckEvent.DefaultPrevented {
			s.repository.logger.Debug().Msg("debridstream: Stream URL received, checking stream file")
			ws.SendEvent(events.DebridStreamState, StreamState{
				Status:      StreamStatusDownloading,
				TorrentName: selectedTorrent.Name,
				Message:     "Checking stream file...",
			})

			retries := 0

		streamUrlCheckLoop:
			for { // Retry loop for a total of 4 times (32 seconds)
				select {
				case <-ctx.Done():
					s.repository.logger.Debug().Msg("debridstream: Context cancelled, stopping stream")
					return
				default:
					// Check if we can stream the URL
					if canStream, reason := CanStream(streamUrl); !canStream {
						if retries >= skipCheckEvent.Retries {
							s.repository.logger.Error().Msg("debridstream: Cannot stream the file")
							s.notifyDirect(slot, DirectStreamFailed)

							ws.SendEvent(events.DebridStreamState, StreamState{
								Status:      StreamStatusFailed,
								TorrentName: selectedTorrent.Name,
								Message:     fmt.Sprintf("Cannot stream this file: %s", reason),
							})
							return
						}
						s.repository.logger.Warn().Msg("debridstream: Rechecking stream file in 8 seconds")
						ws.SendEvent(events.DebridStreamState, StreamState{
							Status:      StreamStatusDownloading,
							TorrentName: selectedTorrent.Name,
							Message:     "Checking stream file...",
						})
						retries++
						time.Sleep(time.Duration(skipCheckEvent.RetryDelay) * time.Second)
						continue
					}
					break streamUrlCheckLoop
				}
			}
		}

		s.repository.logger.Debug().Msg("debridstream: Stream is ready")

		// Signal to the client that the torrent is ready to stream
		ws.SendEvent(events.DebridStreamState, StreamState{
			Status:      StreamStatusReady,
			TorrentName: selectedTorrent.Name,
			Message:     "Ready to stream the file",
		})

		if ctx.Err() != nil {
			s.repository.logger.Debug().Msg("debridstream: Context cancelled, stopping stream")
			ready()
			return
		}

		windowTitle := media.GetPreferredTitle()
		if !media.IsMovieOrSingleEpisode() {
			windowTitle += fmt.Sprintf(" - Episode %s", aniDbEpisode)
		}

		event := &DebridSendStreamToMediaPlayerEvent{
			WindowTitle:  windowTitle,
			StreamURL:    streamUrl,
			Media:        media.ToBaseAnime(),
			AniDbEpisode: aniDbEpisode,
			PlaybackType: string(opts.PlaybackType),
		}
		err = hook.GlobalHookManager.OnDebridSendStreamToMediaPlayer().Trigger(event)
		if err != nil {
			s.repository.logger.Err(err).Msg("debridstream: Failed to send stream to media player")
		}
		windowTitle = event.WindowTitle
		streamUrl = event.StreamURL
		media := event.Media
		aniDbEpisode := event.AniDbEpisode
		playbackType := StreamPlaybackType(event.PlaybackType)

		if event.DefaultPrevented {
			s.repository.logger.Debug().Msg("debridstream: Stream prevented by hook")
			ready()
			return
		}

		slot.setStreamURL(streamUrl)
		s.notifyDirect(slot, DirectStreamReady)

		switch playbackType {
		case PlaybackTypeNone:
			// No playback type selected, just signal to the client that the stream is ready
			ws.SendEvent(events.DebridStreamState, StreamState{
				Status:      StreamStatusReady,
				TorrentName: selectedTorrent.Name,
				Message:     "External player link sent",
			})
		case PlaybackTypeNoneAndAwait:
			// No playback type selected, just signal to the client that the stream is ready
			ws.SendEvent(events.DebridStreamState, StreamState{
				Status:      StreamStatusReady,
				TorrentName: selectedTorrent.Name,
				Message:     "External player link sent",
			})
			ready()

		case PlaybackTypeDefault:
			//
			// Start the stream
			//
			s.repository.logger.Debug().Msg("debridstream: Starting the media player")

			ws.SendEvent(events.InfoToast, "Sending stream to media player...")
			ws.SendEvent(events.ShowIndefiniteLoader, "debridstream")

			playbackSubscriberCtx, playbackSubscriberCancel := context.WithCancel(context.Background())
			slot.setPlaybackSubscriberCancel(playbackSubscriberCancel)
			playbackSubscriber := s.repository.playbackManager.SubscribeToPlaybackStatus("debridstream")

			// Sends the stream to the media player
			// DEVNOTE: Events are handled by the torrentstream.Repository module
			err = s.repository.playbackManager.StartStreamingUsingMediaPlayer(windowTitle, &playbackmanager.StartPlayingOptions{
				Payload:   streamUrl,
				UserAgent: opts.UserAgent,
				ClientId:  opts.ClientId,
			}, media, aniDbEpisode)
			if err != nil {
				go s.repository.playbackManager.UnsubscribeFromPlaybackStatus("debridstream")
				slot.cancelPlaybackSubscriber()
				// Failed to start the stream, we'll drop the torrents and stop the server
				ws.SendEvent(events.DebridStreamState, StreamState{
					Status:      StreamStatusFailed,
					TorrentName: selectedTorrent.Name,
					Message:     fmt.Sprintf("Failed to send the stream to the media player, %v", err),
				})
				return
			}

			// Listen to the playback status
			// Reset the current stream url when playback is stopped
			go func() {
				defer util.HandlePanicInModuleThen("debridstream/PlaybackSubscriber", func() {})
				defer slot.cancelPlaybackSubscriber()
				select {
				case <-playbackSubscriberCtx.Done():
					ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
					s.repository.playbackManager.UnsubscribeFromPlaybackStatus("debridstream")
					slot.setStreamURL("")
				case event := <-playbackSubscriber.EventCh:
					switch event.(type) {
					case playbackmanager.StreamStartedEvent:
						ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
					case playbackmanager.StreamStoppedEvent:
						go s.repository.playbackManager.UnsubscribeFromPlaybackStatus("debridstream")
						slot.setStreamURL("")
					}
				}
			}()

		case PlaybackTypeExternalPlayer:
			// Send the external player link
			ws.SendEventTo(opts.ClientId, events.ExternalPlayerOpenURL, struct {
				Url           string `json:"url"`
				MediaId       int    `json:"mediaId"`
				EpisodeNumber int    `json:"episodeNumber"`
				MediaTitle    string `json:"mediaTitle"`
			}{
				Url:           streamUrl,
				MediaId:       opts.MediaId,
				EpisodeNumber: opts.EpisodeNumber,
				MediaTitle:    media.GetPreferredTitle(),
			}, true) // Garceslab Anime: never log the debrid link

			// Signal to the client that the torrent has started playing (remove loading status)
			// We can't know for sure
			ws.SendEvent(events.DebridStreamState, StreamState{
				Status:      StreamStatusReady,
				TorrentName: selectedTorrent.Name,
				Message:     "External player link sent",
			})
		case PlaybackTypeNativePlayer:
			ws.SendEvent(events.DebridStreamState, StreamState{
				Status:      StreamStatusReady,
				TorrentName: selectedTorrent.Name,
				Message:     "",
			})

			if !s.repository.directStreamManager.IsOpenActive(opts.ClientId) {
				return
			}
			err := s.repository.directStreamManager.PlayDebridStream(ctx, filepath, directstream.PlayDebridStreamOptions{
				StreamUrl:    streamUrl,
				MediaId:      media.ID,
				AnidbEpisode: opts.AniDBEpisode,
				Media:        media,
				Torrent:      selectedTorrent,
				FileId:       fileId,
				UserAgent:    opts.UserAgent,
				ClientId:     opts.ClientId,
				AutoSelect:   false,
			})
			if err != nil {
				s.repository.logger.Error().Err(err).Msg("directstream: Failed to prepare new stream")
				return
			}
		}

		go func() {
			defer util.HandlePanicInModuleThen("debridstream/AddBatchHistory", func() {})

			if selectedTorrent.IsBatch {
				_ = db_bridge.InsertTorrentstreamHistory(s.repository.db, media.GetID(), selectedTorrent, opts.BatchEpisodeFiles)

				ws.SendEvent(events.InvalidateQueries, []string{events.GetTorrentstreamBatchHistoryEndpoint})
			}
		}()
	}(ctx)

	ws.SendEvent(events.DebridStreamState, StreamState{
		Status:      StreamStatusStarted,
		TorrentName: selectedTorrent.Name,
		Message:     "Stream started",
	})
	s.repository.logger.Info().Msg("debridstream: Stream started")

	if opts.PlaybackType == PlaybackTypeNoneAndAwait {
		s.repository.logger.Debug().Msg("debridstream: Waiting for stream to be ready")
		<-readyCh
		ws.SendEvent(events.HideIndefiniteLoader, "debridstream")
	}

	return nil
}

func (s *StreamManager) cancelStream(opts *CancelStreamOptions) {
	if s.repository.directStreamManager != nil {
		s.repository.directStreamManager.CloseOpen("")
	}

	s.mu.Lock()
	slot := s.relayed
	s.relayed = nil
	s.mu.Unlock()

	s.repository.wsEventManager.SendEvent(events.HideIndefiniteLoader, "debridstream")

	s.stopSlot(slot, opts)
}

// cancelClientStream cancels the direct stream of clientId (Garceslab Anime). It
// reports false when the client has no direct stream, so callers can fall back
// to cancelStream (the relayed stream, upstream behaviour).
func (s *StreamManager) cancelClientStream(clientId string, opts *CancelStreamOptions) bool {
	if clientId == "" {
		return false
	}
	s.mu.Lock()
	slot, ok := s.direct[clientId]
	if ok {
		delete(s.direct, clientId)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.eventsFor(slot).SendEvent(events.HideIndefiniteLoader, "debridstream")
	s.stopSlot(slot, opts)
	return true
}

// stopSlot cancels a slot's download and playback subscriber and, if asked,
// removes its torrent from the debrid service.
func (s *StreamManager) stopSlot(slot *streamSlot, opts *CancelStreamOptions) {
	if slot == nil {
		return
	}
	torrentItemId := slot.stop()
	if opts == nil || !opts.RemoveTorrent || torrentItemId == "" {
		return
	}
	provider, err := s.repository.GetProvider()
	if err != nil {
		s.repository.logger.Err(err).Msg("debridstream: Failed to remove torrent")
		return
	}
	if err := provider.DeleteTorrent(torrentItemId); err != nil {
		s.repository.logger.Err(err).Msg("debridstream: Failed to remove torrent")
	}
}

// takeSlot gives the new stream its slot and stops the streams it replaces: a
// direct stream replaces its client's previous stream (direct or relayed); a
// relayed stream replaces the relayed stream (any client) and its client's
// direct stream. Other clients' direct streams are left alone.
func (s *StreamManager) takeSlot(opts *StartStreamOptions) *streamSlot {
	slot := &streamSlot{
		clientId:      opts.ClientId,
		isDirect:      opts.PlaybackType.IsDirect() && opts.ClientId != "",
		mediaId:       opts.MediaId,
		episodeNumber: opts.EpisodeNumber,
	}
	var replaced []*streamSlot
	s.mu.Lock()
	if prev, ok := s.direct[opts.ClientId]; ok && opts.ClientId != "" {
		replaced = append(replaced, prev)
		delete(s.direct, opts.ClientId)
	}
	if slot.isDirect {
		if s.relayed != nil && s.relayed.clientId == opts.ClientId {
			replaced = append(replaced, s.relayed)
			s.relayed = nil
		}
		s.direct[opts.ClientId] = slot
	} else {
		if s.relayed != nil {
			replaced = append(replaced, s.relayed)
		}
		s.relayed = slot
	}
	s.mu.Unlock()
	for _, prev := range replaced {
		prev.stop()
	}
	return slot
}

// eventsFor returns where a slot's status events go: only its client for a
// direct stream, the repository's event manager (broadcast) for a relayed one.
func (s *StreamManager) eventsFor(slot *streamSlot) events.WSEventManagerInterface {
	if slot != nil && slot.isDirect {
		return &clientEvents{WSEventManagerInterface: s.repository.wsEventManager, clientId: slot.clientId}
	}
	return s.repository.wsEventManager
}

// clientEvents turns SendEvent into SendEventTo(clientId).
type clientEvents struct {
	events.WSEventManagerInterface
	clientId string
}

func (c *clientEvents) SendEvent(t string, payload interface{}) {
	c.WSEventManagerInterface.SendEventTo(c.clientId, t, payload)
}

// notifyDirect tells the observer (the viewer sessions registry) how a direct stream went.
func (s *StreamManager) notifyDirect(slot *streamSlot, ev DirectStreamEvent) {
	if slot == nil || !slot.isDirect || slot.isStopped() {
		return
	}
	if f := s.repository.directStreamObserver; f != nil {
		slot.mu.Lock()
		info := DirectStreamInfo{MediaId: slot.mediaId, EpisodeNumber: slot.episodeNumber, MediaTitle: slot.mediaTitle}
		slot.mu.Unlock()
		f(slot.clientId, info, ev)
	}
}

// streamURL returns the stream URL of the client's direct stream, else of the relayed stream.
func (s *StreamManager) streamURL(clientId string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slot, ok := s.direct[clientId]; ok && clientId != "" {
		return slot.streamURL()
	}
	if s.relayed != nil {
		return s.relayed.streamURL()
	}
	return ""
}

// directClientIds returns the clients with a direct stream.
func (s *StreamManager) directClientIds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ret := make([]string, 0, len(s.direct))
	for id := range s.direct {
		ret = append(ret, id)
	}
	return ret
}

// begin records the torrent being streamed and the cancel func of its download
// loop. It reports false when the slot was stopped meanwhile.
func (sl *streamSlot) begin(torrentItemId string, cancel context.CancelFunc) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if sl.stopped {
		return false
	}
	sl.currentTorrentItemId = torrentItemId
	sl.downloadCtxCancelFunc = cancel
	return true
}

func (sl *streamSlot) cancelDownload() {
	sl.mu.Lock()
	cancel := sl.downloadCtxCancelFunc
	sl.downloadCtxCancelFunc = nil
	sl.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (sl *streamSlot) setPlaybackSubscriberCancel(cancel context.CancelFunc) {
	sl.mu.Lock()
	sl.playbackSubscriberCtxCancelFunc = cancel
	sl.mu.Unlock()
}

func (sl *streamSlot) cancelPlaybackSubscriber() {
	sl.mu.Lock()
	cancel := sl.playbackSubscriberCtxCancelFunc
	sl.playbackSubscriberCtxCancelFunc = nil
	sl.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (sl *streamSlot) setStreamURL(u string) {
	sl.mu.Lock()
	sl.currentStreamUrl = u
	sl.mu.Unlock()
}

func (sl *streamSlot) streamURL() string {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.currentStreamUrl
}

func (sl *streamSlot) isStopped() bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.stopped
}

// stop cancels the slot's goroutines and returns its torrent item id.
func (sl *streamSlot) stop() string {
	sl.mu.Lock()
	sl.stopped = true
	cancel := sl.downloadCtxCancelFunc
	sub := sl.playbackSubscriberCtxCancelFunc
	sl.downloadCtxCancelFunc = nil
	sl.playbackSubscriberCtxCancelFunc = nil
	sl.currentStreamUrl = ""
	id := sl.currentTorrentItemId
	sl.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if sub != nil {
		sub()
	}
	return id
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
