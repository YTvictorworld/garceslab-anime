// Package sessions keeps the viewer sessions of phase 2 (docs/phase2-direct-play.md):
// up to Max people watching at the same time, one session per client, each
// playing a debrid link directly on its device (the server only resolves the
// link). A session holds one "viewing slot"; a new one gets LANES_FULL when
// every slot is taken. Sessions end when their client stops, when the client's
// websocket stays closed for the disconnect grace, after the idle timeout,
// when the same profile starts watching on another device, or from Settings.
//
// The registry knows nothing about debrid or players: core wires OnRelease (to
// cancel the client's pending debrid resolve) and RelayedBusy (the single
// relayed playback lane, which also counts as a viewer).
package sessions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrLanesFull is returned (wrapped in *FullError) when every viewing slot is in use.
var ErrLanesFull = errors.New("LANES_FULL")

// ErrNoClientID: a session needs the client id of the device.
var ErrNoClientID = errors.New("VIEWER_NO_CLIENT_ID")

// ErrNotFound: no session with that id.
var ErrNotFound = errors.New("VIEWER_SESSION_NOT_FOUND")

const (
	DefaultMax             = 3
	DefaultDisconnectGrace = 45 * time.Second
	DefaultIdleTimeout     = 15 * time.Minute

	// EnvMaxViewers sets the number of viewing slots (default 3).
	EnvMaxViewers = "SEANIME_MAX_VIEWERS"
	// EnvIdleTimeout sets the idle timeout (Go duration, e.g. "15m").
	EnvIdleTimeout = "SEANIME_VIEWER_IDLE_TIMEOUT"
	// EnvDisconnectGrace sets how long a closed websocket keeps its slot (e.g. "45s").
	EnvDisconnectGrace = "SEANIME_VIEWER_DISCONNECT_GRACE"
)

// Websocket events sent to the owner client only.
const (
	// EventSessionEnded tells a client that the server ended its session
	// (payload: SessionEndedPayload). Not sent when the client stopped it.
	EventSessionEnded = "viewer-session-ended"
	// Upstream's manual tracking events, sent per session.
	EventManualTrackingState   = "playback-manager-manual-tracking-playback-state"
	EventManualTrackingStopped = "playback-manager-manual-tracking-stopped"
)

// FullError is ErrLanesFull with who holds the slots.
type FullError struct {
	Max     int
	Viewers []string // profile names (or "Guest"), sorted
}

func (e *FullError) Error() string {
	msg := fmt.Sprintf("LANES_FULL: All %d viewing slots are in use", e.Max)
	if len(e.Viewers) > 0 {
		msg += " (" + joinNames(e.Viewers) + ")"
	}
	return msg
}

func (e *FullError) Is(target error) bool { return target == ErrLanesFull }

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// ViewerState is the state of a session.
type ViewerState string

const (
	StatePreparing ViewerState = "preparing" // the server is resolving the debrid link
	StateReady     ViewerState = "ready"     // the link was sent to the client
	StatePlaying   ViewerState = "playing"   // the client's player started (manual tracking)
)

// Reason a session ended.
type Reason string

const (
	ReasonStopped      Reason = "stopped"      // the client stopped it
	ReasonReplaced     Reason = "replaced"     // the same profile started watching on another device
	ReasonDisconnected Reason = "disconnected" // websocket closed for longer than the grace
	ReasonIdle         Reason = "idle"         // no request from the client for the idle timeout
	ReasonAdmin        Reason = "admin"        // stopped from Settings → Profiles
	ReasonFailed       Reason = "failed"       // the debrid link could not be resolved
	ReasonProfileGone  Reason = "profile-gone" // the profile was deleted
	ReasonNewSession   Reason = "new-session"  // the client started another episode
)

// ViewerTracking is the per-session manual progress tracking (upstream's
// playbackmanager manual tracking, which is global).
type ViewerTracking struct {
	MediaID         int    `json:"mediaId"`
	EpisodeNumber   int    `json:"episodeNumber"`
	MediaTitle      string `json:"mediaTitle"`
	TotalEpisodes   int    `json:"totalEpisodes"`
	CurrentProgress int    `json:"currentProgress"`
}

// ViewerSession is a snapshot of one viewer session.
type ViewerSession struct {
	ID          string          `json:"id"`
	ClientID    string          `json:"-"`
	ProfileID   int64           `json:"profileId"`
	ProfileName string          `json:"profileName"`
	Device      string          `json:"device"`
	MediaID     int             `json:"mediaId"`
	Episode     int             `json:"episodeNumber"`
	MediaTitle  string          `json:"mediaTitle"`
	State       ViewerState     `json:"state"`
	StartedAt   time.Time       `json:"startedAt"`
	LastSeen    time.Time       `json:"lastSeen"`
	Tracking    *ViewerTracking `json:"tracking,omitempty"`
	// Disconnected is set while the client's websocket is closed (grace running).
	Disconnected bool `json:"disconnected"`
}

// SessionEndedPayload is the payload of EventSessionEnded.
type SessionEndedPayload struct {
	SessionID string `json:"sessionId"`
	Reason    Reason `json:"reason"`
	MediaID   int    `json:"mediaId"`
	Episode   int    `json:"episodeNumber"`
}

// AcquireRequest asks for a slot.
type AcquireRequest struct {
	ClientID    string
	ProfileID   int64 // 0: a client without profile
	ProfileName string
	Device      string
	MediaID     int
	Episode     int
	MediaTitle  string
}

// EventSender is the part of the websocket event manager the registry uses.
type EventSender interface {
	SendEventTo(clientId string, t string, payload interface{}, noLog ...bool)
}

// Options configure a Registry. Zero values take the defaults.
type Options struct {
	Max             int
	DisconnectGrace time.Duration
	IdleTimeout     time.Duration
	Events          EventSender
	// OnRelease runs (outside the lock) after a session ends, e.g. to cancel the
	// client's debrid resolve. Not run for ReasonNewSession: the new stream
	// replaces the old one itself.
	OnRelease func(s ViewerSession, reason Reason)
	// RelayedBusy reports the profile holding the relayed playback lane (single,
	// server-relayed playback), which also takes a viewing slot.
	RelayedBusy func() (profileID int64, name string, busy bool)
	Now         func() time.Time
}

// OptionsFromEnv returns the options set by SEANIME_MAX_VIEWERS,
// SEANIME_VIEWER_IDLE_TIMEOUT and SEANIME_VIEWER_DISCONNECT_GRACE.
func OptionsFromEnv() Options {
	var o Options
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(EnvMaxViewers))); err == nil && v > 0 {
		o.Max = v
	}
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(EnvIdleTimeout))); err == nil && d > 0 {
		o.IdleTimeout = d
	}
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(EnvDisconnectGrace))); err == nil && d >= 0 {
		o.DisconnectGrace = d
	}
	return o
}

type session struct {
	ViewerSession
	disconnectedAt time.Time
}

// Registry holds the viewer sessions.
type Registry struct {
	opts Options

	mu       sync.Mutex
	byClient map[string]*session
}

// New returns a registry.
func New(opts Options) *Registry {
	if opts.Max <= 0 {
		opts.Max = DefaultMax
	}
	if opts.DisconnectGrace == 0 {
		opts.DisconnectGrace = DefaultDisconnectGrace
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Registry{opts: opts, byClient: map[string]*session{}}
}

// Max returns the number of viewing slots.
func (r *Registry) Max() int { return r.opts.Max }

// SetOnRelease sets OnRelease (core sets it once the modules exist).
func (r *Registry) SetOnRelease(f func(s ViewerSession, reason Reason)) {
	r.mu.Lock()
	r.opts.OnRelease = f
	r.mu.Unlock()
}

// SetRelayedBusy sets RelayedBusy.
func (r *Registry) SetRelayedBusy(f func() (int64, string, bool)) {
	r.mu.Lock()
	r.opts.RelayedBusy = f
	r.mu.Unlock()
}

// SetEvents sets the event sender.
func (r *Registry) SetEvents(e EventSender) {
	r.mu.Lock()
	r.opts.Events = e
	r.mu.Unlock()
}

type ended struct {
	s      ViewerSession
	reason Reason
}

// Acquire gives the client a viewing slot for a new episode. The client's
// previous session (if any) is replaced, as is the session of the same profile
// on another device. Fails with *FullError when every slot is taken.
func (r *Registry) Acquire(req AcquireRequest) (ViewerSession, error) {
	if strings.TrimSpace(req.ClientID) == "" {
		return ViewerSession{}, ErrNoClientID
	}
	relayedPID, relayedName, relayedBusy := int64(0), "", false
	r.mu.Lock()
	relayedFn := r.opts.RelayedBusy
	r.mu.Unlock()
	if relayedFn != nil { // outside the lock: the lane may ask the registry back
		relayedPID, relayedName, relayedBusy = relayedFn()
	}

	r.mu.Lock()
	now := r.opts.Now()
	var released []ended
	r.expireLocked(now, &released)

	// Sessions this one replaces don't count.
	others := make([]*session, 0, len(r.byClient))
	for id, s := range r.byClient {
		if id == req.ClientID || (req.ProfileID > 0 && s.ProfileID == req.ProfileID) {
			continue
		}
		others = append(others, s)
	}
	used := len(others)
	names := make([]string, 0, used+1)
	for _, s := range others {
		names = append(names, displayName(s.ProfileName))
	}
	if relayedBusy && (req.ProfileID <= 0 || relayedPID != req.ProfileID) {
		used++
		names = append(names, displayName(relayedName))
	}
	if used >= r.opts.Max {
		r.mu.Unlock()
		r.finish(released)
		sort.Strings(names)
		return ViewerSession{}, &FullError{Max: r.opts.Max, Viewers: names}
	}

	for id, s := range r.byClient {
		switch {
		case id == req.ClientID:
			released = append(released, ended{s.ViewerSession, ReasonNewSession})
			delete(r.byClient, id)
		case req.ProfileID > 0 && s.ProfileID == req.ProfileID:
			released = append(released, ended{s.ViewerSession, ReasonReplaced})
			delete(r.byClient, id)
		}
	}
	s := &session{ViewerSession: ViewerSession{
		ID:          newID(),
		ClientID:    req.ClientID,
		ProfileID:   req.ProfileID,
		ProfileName: req.ProfileName,
		Device:      req.Device,
		MediaID:     req.MediaID,
		Episode:     req.Episode,
		MediaTitle:  req.MediaTitle,
		State:       StatePreparing,
		StartedAt:   now,
		LastSeen:    now,
	}}
	r.byClient[req.ClientID] = s
	snap := s.snapshot()
	r.mu.Unlock()
	r.finish(released)
	return snap, nil
}

// Capacity reports whether a viewer of profileID (relayed playback) fits: it
// fails with *FullError when the direct sessions of other profiles fill every
// slot. The relayed lane itself is not counted (the caller is taking it).
func (r *Registry) Capacity(profileID int64, clientID string) error {
	r.mu.Lock()
	now := r.opts.Now()
	var released []ended
	r.expireLocked(now, &released)
	names := make([]string, 0)
	for id, s := range r.byClient {
		if id == clientID || (profileID > 0 && s.ProfileID == profileID) {
			continue
		}
		names = append(names, displayName(s.ProfileName))
	}
	max := r.opts.Max
	r.mu.Unlock()
	r.finish(released)
	if len(names) >= max {
		sort.Strings(names)
		return &FullError{Max: max, Viewers: names}
	}
	return nil
}

// Get returns the client's session.
func (r *Registry) Get(clientID string) (ViewerSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byClient[clientID]
	if !ok {
		return ViewerSession{}, false
	}
	return s.snapshot(), true
}

// List returns every session, oldest first.
func (r *Registry) List() []ViewerSession {
	r.mu.Lock()
	now := r.opts.Now()
	var released []ended
	r.expireLocked(now, &released)
	ret := make([]ViewerSession, 0, len(r.byClient))
	for _, s := range r.byClient {
		ret = append(ret, s.snapshot())
	}
	r.mu.Unlock()
	r.finish(released)
	sort.Slice(ret, func(i, j int) bool {
		if ret[i].StartedAt.Equal(ret[j].StartedAt) {
			return ret[i].ID < ret[j].ID
		}
		return ret[i].StartedAt.Before(ret[j].StartedAt)
	})
	return ret
}

// Count returns the number of sessions.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byClient)
}

// Touch records activity of the client (any authenticated request).
func (r *Registry) Touch(clientID string) {
	if clientID == "" {
		return
	}
	r.mu.Lock()
	if s, ok := r.byClient[clientID]; ok {
		s.LastSeen = r.opts.Now()
	}
	r.mu.Unlock()
}

// SetState moves the client's session to state (only for the given episode,
// when mediaID > 0, so a late event of a previous episode changes nothing).
func (r *Registry) SetState(clientID string, mediaID, episode int, state ViewerState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byClient[clientID]
	if !ok || !s.matches(mediaID, episode) {
		return false
	}
	s.State = state
	s.LastSeen = r.opts.Now()
	return true
}

// SetTitle records the anime title of the client's session (for "Now watching").
func (r *Registry) SetTitle(clientID string, mediaID, episode int, title string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.byClient[clientID]; ok && s.matches(mediaID, episode) && title != "" {
		s.MediaTitle = title
	}
}

// Release ends the client's session. With mediaID > 0 only if the session is
// for that episode (a late "stop" of the previous episode keeps the new one).
func (r *Registry) Release(clientID string, mediaID, episode int, reason Reason) bool {
	r.mu.Lock()
	s, ok := r.byClient[clientID]
	if !ok || !s.matches(mediaID, episode) {
		r.mu.Unlock()
		return false
	}
	delete(r.byClient, clientID)
	snap := s.snapshot()
	r.mu.Unlock()
	r.finish([]ended{{snap, reason}})
	return true
}

// ReleaseByID ends a session by id (Settings → Profiles → Stop).
func (r *Registry) ReleaseByID(id string, reason Reason) (ViewerSession, error) {
	r.mu.Lock()
	for cid, s := range r.byClient {
		if s.ID == id {
			delete(r.byClient, cid)
			snap := s.snapshot()
			r.mu.Unlock()
			r.finish([]ended{{snap, reason}})
			return snap, nil
		}
	}
	r.mu.Unlock()
	return ViewerSession{}, ErrNotFound
}

// ForgetProfile ends the sessions of a deleted profile.
func (r *Registry) ForgetProfile(profileID int64) {
	if profileID <= 0 {
		return
	}
	r.mu.Lock()
	var released []ended
	for cid, s := range r.byClient {
		if s.ProfileID == profileID {
			delete(r.byClient, cid)
			released = append(released, ended{s.snapshot(), ReasonProfileGone})
		}
	}
	r.mu.Unlock()
	r.finish(released)
}

// ClientConnected cancels a pending disconnect release (the websocket came back).
func (r *Registry) ClientConnected(clientID string) {
	r.mu.Lock()
	if s, ok := r.byClient[clientID]; ok {
		s.disconnectedAt = time.Time{}
		s.Disconnected = false
		s.LastSeen = r.opts.Now()
	}
	r.mu.Unlock()
}

// ClientDisconnected starts the disconnect grace of the client's session; the
// slot is freed by Sweep if the client doesn't reconnect in time.
func (r *Registry) ClientDisconnected(clientID string) {
	r.mu.Lock()
	if s, ok := r.byClient[clientID]; ok && s.disconnectedAt.IsZero() {
		s.disconnectedAt = r.opts.Now()
		s.Disconnected = true
	}
	grace := r.opts.DisconnectGrace
	r.mu.Unlock()
	if grace <= 0 {
		r.Sweep()
	}
}

// Sweep frees the slots of disconnected and idle sessions.
func (r *Registry) Sweep() {
	r.mu.Lock()
	var released []ended
	r.expireLocked(r.opts.Now(), &released)
	r.mu.Unlock()
	r.finish(released)
}

// Run sweeps every interval until ctx ends.
func (r *Registry) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Sweep()
		}
	}
}

// StartTracking starts the session's manual progress tracking (the client's
// player opened the episode) and marks it playing. It fails if the client has
// no session.
func (r *Registry) StartTracking(clientID string, t ViewerTracking) (ViewerSession, bool) {
	r.mu.Lock()
	s, ok := r.byClient[clientID]
	if !ok {
		r.mu.Unlock()
		return ViewerSession{}, false
	}
	tr := t
	s.Tracking = &tr
	s.MediaID, s.Episode = t.MediaID, t.EpisodeNumber
	if t.MediaTitle != "" {
		s.MediaTitle = t.MediaTitle
	}
	s.State = StatePlaying
	s.LastSeen = r.opts.Now()
	snap := s.snapshot()
	ev := r.opts.Events
	r.mu.Unlock()
	if ev != nil {
		ev.SendEventTo(clientID, EventManualTrackingState, trackingState(t), true)
	}
	return snap, true
}

// Tracking returns the client's manual tracking state.
func (r *Registry) Tracking(clientID string) (ViewerTracking, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byClient[clientID]
	if !ok || s.Tracking == nil {
		return ViewerTracking{}, false
	}
	return *s.Tracking, true
}

// HasTracking reports whether the client has a session with manual tracking.
func (r *Registry) HasTracking(clientID string) bool {
	_, ok := r.Tracking(clientID)
	return ok
}

// trackingState is upstream's PlaybackState shape for manual tracking.
func trackingState(t ViewerTracking) map[string]interface{} {
	return map[string]interface{}{
		"episodeNumber":        t.EpisodeNumber,
		"mediaTitle":           t.MediaTitle,
		"mediaTotalEpisodes":   t.TotalEpisodes,
		"filename":             "",
		"completionPercentage": 0,
		"canPlayNext":          false,
		"progressUpdated":      false,
		"mediaId":              t.MediaID,
	}
}

func (r *Registry) expireLocked(now time.Time, out *[]ended) {
	for cid, s := range r.byClient {
		switch {
		case !s.disconnectedAt.IsZero() && now.Sub(s.disconnectedAt) >= r.opts.DisconnectGrace:
			delete(r.byClient, cid)
			*out = append(*out, ended{s.snapshot(), ReasonDisconnected})
		case now.Sub(s.LastSeen) >= r.opts.IdleTimeout:
			delete(r.byClient, cid)
			*out = append(*out, ended{s.snapshot(), ReasonIdle})
		}
	}
}

// finish tells the owners and runs OnRelease, outside the lock.
func (r *Registry) finish(list []ended) {
	if len(list) == 0 {
		return
	}
	r.mu.Lock()
	ev, onRelease := r.opts.Events, r.opts.OnRelease
	r.mu.Unlock()
	for _, e := range list {
		if ev != nil {
			if e.s.Tracking != nil && e.reason != ReasonNewSession {
				ev.SendEventTo(e.s.ClientID, EventManualTrackingStopped, nil, true)
			}
			if e.reason != ReasonStopped && e.reason != ReasonNewSession {
				ev.SendEventTo(e.s.ClientID, EventSessionEnded, SessionEndedPayload{
					SessionID: e.s.ID, Reason: e.reason, MediaID: e.s.MediaID, Episode: e.s.Episode,
				}, true)
			}
		}
		if onRelease != nil && e.reason != ReasonNewSession {
			onRelease(e.s, e.reason)
		}
	}
}

func (s *session) matches(mediaID, episode int) bool {
	if mediaID <= 0 {
		return true
	}
	return s.MediaID == mediaID && (episode <= 0 || s.Episode == episode)
}

func (s *session) snapshot() ViewerSession {
	c := s.ViewerSession
	if s.Tracking != nil {
		t := *s.Tracking
		c.Tracking = &t
	}
	return c
}

func displayName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "Guest"
	}
	return name
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// DeviceLabel is a short, user-facing name for a device from its client
// platform and user agent (shown in "Now watching").
func DeviceLabel(platform, userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "web0s") || strings.Contains(ua, "webos"):
		return "LG TV"
	case strings.Contains(ua, "tizen"):
		return "Samsung TV"
	case strings.Contains(ua, "android"):
		if strings.Contains(ua, "tv") || strings.Contains(ua, "aft") {
			return "Android TV"
		}
		return "Android"
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad"):
		return "iOS"
	}
	switch strings.ToLower(platform) {
	case "denshi":
		return "Desktop app"
	case "mobile":
		return "Mobile app"
	case "web", "":
		if ua != "" {
			return "Browser"
		}
	}
	if platform == "" {
		return "Unknown device"
	}
	return platform
}
