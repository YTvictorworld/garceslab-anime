package profiles

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrPlaybackBusy is returned when another profile is using the (single, phase 1)
// playback lane.
var ErrPlaybackBusy = errors.New("PLAYBACK_BUSY")

// laneGrace keeps the lane reserved for its owner right after a playback request,
// while the stream is still being prepared (torrent selection, debrid caching...)
// and no player reports activity yet.
const laneGrace = 90 * time.Second

// PlaybackLane is the single global playback lane of phase 1 (plan §5 Fase 1): one
// playback at a time on the whole server. The profile that starts a playback owns
// the lane, so the progress, continuity and "mark as watching" of the server-side
// players (which run without a request context) go to that profile. Phase 2 turns
// this into one lane per profile.
type PlaybackLane struct {
	mu         sync.RWMutex
	owner      *Profile
	acquiredAt time.Time
	active     func() bool
	onAcquire  func(p *Profile)
	now        func() time.Time
	// capacity (phase 2) fails when the direct viewer sessions of other profiles
	// fill every viewing slot (sessions.Registry.Capacity).
	capacity func(p *Profile, clientID string) error
}

func NewPlaybackLane() *PlaybackLane {
	return &PlaybackLane{now: time.Now}
}

// SetActiveFunc sets the function that reports whether something is playing
// (provided by core: media players, direct stream, playback manager).
func (l *PlaybackLane) SetActiveFunc(f func() bool) {
	l.mu.Lock()
	l.active = f
	l.mu.Unlock()
}

func (l *PlaybackLane) isBusyFor(p *Profile) bool {
	if l.owner == nil && l.acquiredAt.IsZero() {
		return false
	}
	if p != nil && l.owner != nil && l.owner.ID == p.ID {
		return false
	}
	if p == nil && l.owner == nil {
		return false
	}
	if l.now().Sub(l.acquiredAt) < laneGrace {
		return true
	}
	return l.active != nil && l.active()
}

// SetOnAcquire sets a callback run after the lane changes hands or is renewed
// (core pushes the owner's collection to the playback modules).
func (l *PlaybackLane) SetOnAcquire(f func(p *Profile)) {
	l.mu.Lock()
	l.onAcquire = f
	l.mu.Unlock()
}

// SetCapacityFunc sets the phase 2 viewing-slot check (core: the viewer sessions).
func (l *PlaybackLane) SetCapacityFunc(f func(p *Profile, clientID string) error) {
	l.mu.Lock()
	l.capacity = f
	l.mu.Unlock()
}

// Acquire gives the lane to p (nil = a client without profile, whose progress is
// not saved). It fails with ErrPlaybackBusy if another profile is playing.
func (l *PlaybackLane) Acquire(p *Profile) error { return l.AcquireFor(p, "") }

// AcquireFor is Acquire for a client: since phase 2 the relayed lane is one of
// the viewing slots, so it also fails (LANES_FULL) when the direct sessions of
// other profiles and clients take every slot.
func (l *PlaybackLane) AcquireFor(p *Profile, clientID string) error {
	l.mu.RLock()
	capacity := l.capacity
	l.mu.RUnlock()
	if capacity != nil { // outside the lock: the registry asks the lane back
		if err := capacity(p, clientID); err != nil {
			return err
		}
	}
	l.mu.Lock()
	if l.isBusyFor(p) {
		l.mu.Unlock()
		return ErrPlaybackBusy
	}
	l.owner = p
	l.acquiredAt = l.now()
	cb := l.onAcquire
	l.mu.Unlock()
	if cb != nil {
		cb(p)
	}
	return nil
}

// ActiveOwner reports whether a relayed playback holds the lane right now (being
// prepared, or the media core has a session) and the profile that owns it.
func (l *PlaybackLane) ActiveOwner() (*Profile, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.RLock()
	owner, acquiredAt, active := l.owner, l.acquiredAt, l.active
	l.mu.RUnlock()
	if acquiredAt.IsZero() {
		return nil, false
	}
	if l.now().Sub(acquiredAt) < laneGrace {
		return owner, true
	}
	if active != nil && active() {
		return owner, true
	}
	return nil, false
}

// Owner returns the profile that owns the lane (nil if none).
func (l *PlaybackLane) Owner() *Profile {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.owner
}

// OwnerID returns the id of the lane owner (0 if none).
func (l *PlaybackLane) OwnerID() int64 {
	if p := l.Owner(); p != nil {
		return p.ID
	}
	return 0
}

// Context returns ctx with the lane owner when ctx has no profile of its own.
// Server-side playback code (no request context) goes through this.
func (l *PlaybackLane) Context(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if FromContext(ctx) != nil {
		return ctx
	}
	if p := l.Owner(); p != nil {
		return WithProfile(ctx, p)
	}
	return ctx
}

// ForgetProfile releases the lane if p owns it (profile deleted).
func (l *PlaybackLane) ForgetProfile(id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner != nil && l.owner.ID == id {
		l.owner = nil
		l.acquiredAt = time.Time{}
	}
}

// playbackStartRoutes start a server-side playback (they take the lane). Online
// streaming is played by the client and saves progress with its own requests.
// Phase 2: debrid stream starts and manual tracking take the lane in their
// handlers instead, only when the playback is relayed (a direct debrid stream
// takes a viewer session, internal/profiles/sessions).
var playbackStartRoutes = map[string]bool{
	"POST /api/v1/playback-manager/play":                  true,
	"POST /api/v1/playback-manager/play-random":           true,
	"POST /api/v1/playback-manager/start-playlist":        true,
	"POST /api/v1/playback-manager/playlist-next":         true,
	"POST /api/v1/playback-manager/next-episode":          true,
	"POST /api/v1/playback-manager/autoplay-next-episode": true,
	"POST /api/v1/directstream/play/localfile":            true,
	"POST /api/v1/torrentstream/start":                    true,
	"POST /api/v1/mediastream/request":                    true,
}

// IsPlaybackStartRoute reports whether the route starts a server-side playback.
func IsPlaybackStartRoute(method, route string) bool {
	return playbackStartRoutes[strings.ToUpper(method)+" "+route]
}
