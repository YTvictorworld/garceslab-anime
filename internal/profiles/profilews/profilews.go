// Package profilews routes websocket events per profile (plan §3.7.1, §3.7.2).
// Connections are tagged with the profile of their client (from the /events
// ?profile= token and from every authenticated HTTP request of the same client id),
// and ProfileWS sends "broadcast" events only to the clients of one profile.
package profilews

import (
	"seanime/internal/events"
	"sort"
	"sync"
)

// Tags maps websocket client ids to profiles.
type Tags struct {
	mu   sync.RWMutex
	byID map[string]int64
}

func NewTags() *Tags { return &Tags{byID: map[string]int64{}} }

// Tag records that clientID uses profileID (0 untags).
func (t *Tags) Tag(clientID string, profileID int64) {
	if clientID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if profileID <= 0 {
		delete(t.byID, clientID)
		return
	}
	t.byID[clientID] = profileID
}

// Untag forgets a client (logout, profile deleted).
func (t *Tags) Untag(clientID string) { t.Tag(clientID, 0) }

// UntagProfile forgets every client of a profile.
func (t *Tags) UntagProfile(profileID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, p := range t.byID {
		if p == profileID {
			delete(t.byID, id)
		}
	}
}

// ProfileOf returns the profile of a client (0 if unknown).
func (t *Tags) ProfileOf(clientID string) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.byID[clientID]
}

// ClientsOf returns the connected clients of a profile, among connected.
func (t *Tags) ClientsOf(profileID int64, connected []string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ret := make([]string, 0)
	for _, id := range connected {
		if profileID > 0 && t.byID[id] == profileID {
			ret = append(ret, id)
		}
	}
	sort.Strings(ret)
	return ret
}

// ProfileWS implements events.WSEventManagerInterface for the components of one
// profile (or of the playback lane): SendEvent goes only to that profile's
// clients. Targeted events and client event subscriptions are passed through.
type ProfileWS struct {
	base    events.WSEventManagerInterface
	tags    *Tags
	profile func() int64
	// BroadcastWithoutProfile sends to everyone when profile() is 0 (a playback
	// started by a client without profile carries no profile data).
	BroadcastWithoutProfile bool
}

var _ events.WSEventManagerInterface = (*ProfileWS)(nil)

// New returns a ProfileWS for the profile returned by profile (evaluated on each event).
func New(base events.WSEventManagerInterface, tags *Tags, profile func() int64) *ProfileWS {
	return &ProfileWS{base: base, tags: tags, profile: profile}
}

// ForProfile returns a ProfileWS bound to a fixed profile.
func ForProfile(base events.WSEventManagerInterface, tags *Tags, profileID int64) *ProfileWS {
	return New(base, tags, func() int64 { return profileID })
}

func (w *ProfileWS) SendEvent(t string, payload interface{}) {
	pid := w.profile()
	if pid <= 0 {
		if w.BroadcastWithoutProfile {
			w.base.SendEvent(t, payload)
		}
		return
	}
	for _, id := range w.tags.ClientsOf(pid, w.base.GetClientIds()) {
		w.base.SendEventTo(id, t, payload, true)
	}
}

func (w *ProfileWS) SendEventTo(clientId string, t string, payload interface{}, noLog ...bool) {
	w.base.SendEventTo(clientId, t, payload, noLog...)
}

func (w *ProfileWS) GetClientIds() []string { return w.base.GetClientIds() }

func (w *ProfileWS) GetClientPlatform(clientId string) string {
	return w.base.GetClientPlatform(clientId)
}

func (w *ProfileWS) SubscribeToClientEvents(id string) *events.ClientEventSubscriber {
	return w.base.SubscribeToClientEvents(id)
}

func (w *ProfileWS) SubscribeToClientNativePlayerEvents(id string) *events.ClientEventSubscriber {
	return w.base.SubscribeToClientNativePlayerEvents(id)
}

func (w *ProfileWS) SubscribeToClientVideoCoreEvents(id string) *events.ClientEventSubscriber {
	return w.base.SubscribeToClientVideoCoreEvents(id)
}

func (w *ProfileWS) SubscribeToClientMpvCoreEvents(id string) *events.ClientEventSubscriber {
	return w.base.SubscribeToClientMpvCoreEvents(id)
}

func (w *ProfileWS) SubscribeToClientNakamaEvents(id string) *events.ClientEventSubscriber {
	return w.base.SubscribeToClientNakamaEvents(id)
}

func (w *ProfileWS) SubscribeToClientPlaylistEvents(id string) *events.ClientEventSubscriber {
	return w.base.SubscribeToClientPlaylistEvents(id)
}

func (w *ProfileWS) UnsubscribeFromClientEvents(id string) {
	w.base.UnsubscribeFromClientEvents(id)
}
