package settings

// Garceslab Anime (phase 4): player preferences per profile (plan §3.7.3).
// They used to live only on each device (MMKV on the TV, localStorage on the
// web). They are stored in profile_settings.value.player, one section per
// client, so they follow the profile from one device to another. Device-bound
// settings (video output, external player, gestures, API keys) stay on the
// device and are not accepted here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
)

// TVPlayerPrefs are the TV app's player preferences that follow the profile.
// Every field is optional: nil = the app's default (or its local value).
// SkipFillerEpisodes: "next episode" jumps over filler and recap episodes
// when the anime's episode tags are known (Garceslab Anime).
type TVPlayerPrefs struct {
	PreferredAudioLanguages    *string  `json:"preferredAudioLanguages,omitempty"`
	PreferredSubtitleLanguages *string  `json:"preferredSubtitleLanguages,omitempty"`
	IgnoredSubtitleLabels      *string  `json:"ignoredSubtitleLabels,omitempty"`
	ShowSubtitles              *bool    `json:"showSubtitles,omitempty"`
	SubtitleFontSize           *int     `json:"subtitleFontSize,omitempty"`
	AssRendering               *bool    `json:"assRendering,omitempty"`
	AutoPlay                   *bool    `json:"autoPlay,omitempty"`
	AutoNextEpisode            *bool    `json:"autoNextEpisode,omitempty"`
	SkipFillerEpisodes         *bool    `json:"skipFillerEpisodes,omitempty"`
	AutoSkipOpEd               *bool    `json:"autoSkipOpEd,omitempty"`
	SkipDurationSec            *int     `json:"skipDurationSec,omitempty"`
	ButtonSeekSec              *int     `json:"buttonSeekSec,omitempty"`
	DoubleTapSeekSec           *int     `json:"doubleTapSeekSec,omitempty"`
	Speed                      *float64 `json:"speed,omitempty"`
	LongPressFastForwardSpeed  *float64 `json:"longPressFastForwardSpeed,omitempty"`
}

// WebPlayerPrefs are the web player's preferences: the video core settings
// (languages, subtitle and caption styles, video enhancement) and the media
// core preferences (autoplay, auto next, auto skip, volume, speed). They are
// stored as the web app writes them (JSON objects, size-capped).
type WebPlayerPrefs struct {
	VideoCore json.RawMessage `json:"videoCore,omitempty"`
	MediaCore json.RawMessage `json:"mediaCore,omitempty"`
}

// PlayerPrefs is profile_settings.value.player.
type PlayerPrefs struct {
	TV        *TVPlayerPrefs  `json:"tv,omitempty"`
	Web       *WebPlayerPrefs `json:"web,omitempty"`
	UpdatedAt int64           `json:"updatedAt,omitempty"`
}

const maxPrefString = 200

func checkString(name string, v *string) error {
	if v != nil && len(*v) > maxPrefString {
		return fmt.Errorf("%w: player.%s is too long", ErrInvalidSetting, name)
	}
	return nil
}

func checkInt(name string, v *int, min, max int) error {
	if v != nil && (*v < min || *v > max) {
		return fmt.Errorf("%w: player.%s must be between %d and %d", ErrInvalidSetting, name, min, max)
	}
	return nil
}

func checkFloat(name string, v *float64, min, max float64) error {
	if v != nil && (math.IsNaN(*v) || *v < min || *v > max) {
		return fmt.Errorf("%w: player.%s must be between %g and %g", ErrInvalidSetting, name, min, max)
	}
	return nil
}

func checkObject(name string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return fmt.Errorf("%w: player.%s must be a JSON object", ErrInvalidSetting, name)
	}
	return nil
}

// Validate checks types and ranges (the TV app's own limits).
func (p *PlayerPrefs) Validate() error {
	if p == nil {
		return nil
	}
	if t := p.TV; t != nil {
		for _, err := range []error{
			checkString("tv.preferredAudioLanguages", t.PreferredAudioLanguages),
			checkString("tv.preferredSubtitleLanguages", t.PreferredSubtitleLanguages),
			checkString("tv.ignoredSubtitleLabels", t.IgnoredSubtitleLabels),
			checkInt("tv.subtitleFontSize", t.SubtitleFontSize, 10, 120),
			checkInt("tv.skipDurationSec", t.SkipDurationSec, 0, 600),
			checkInt("tv.buttonSeekSec", t.ButtonSeekSec, 1, 600),
			checkInt("tv.doubleTapSeekSec", t.DoubleTapSeekSec, 1, 600),
			checkFloat("tv.speed", t.Speed, 0.25, 4),
			checkFloat("tv.longPressFastForwardSpeed", t.LongPressFastForwardSpeed, 1, 8),
		} {
			if err != nil {
				return err
			}
		}
	}
	if w := p.Web; w != nil {
		if err := checkObject("web.videoCore", w.VideoCore); err != nil {
			return err
		}
		if err := checkObject("web.mediaCore", w.MediaCore); err != nil {
			return err
		}
	}
	return nil
}

// ParsePlayerPrefs reads stored player preferences. Unknown keys are dropped.
func ParsePlayerPrefs(raw json.RawMessage) (*PlayerPrefs, error) {
	ret := &PlayerPrefs{}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return ret, nil
	}
	if err := json.Unmarshal(raw, ret); err != nil {
		return nil, fmt.Errorf("%w: player preferences: %v", ErrInvalidSetting, err)
	}
	return ret, nil
}

// PlayerPrefsPatch is a partial update: a section that is sent replaces the
// fields it contains (TV: the non-null fields; web: videoCore/mediaCore as a
// whole), `"tv": null` / `"web": null` clears that section.
type PlayerPrefsPatch map[string]json.RawMessage

// Apply returns cur with the patch applied.
func (patch PlayerPrefsPatch) Apply(cur *PlayerPrefs) (*PlayerPrefs, error) {
	next := cur.clonePrefs()
	for key, raw := range patch {
		isNull := string(bytes.TrimSpace(raw)) == "null"
		switch key {
		case "tv":
			if isNull {
				next.TV = nil
				continue
			}
			var in TVPlayerPrefs
			if err := strictUnmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("%w: player.tv: %v", ErrInvalidSetting, err)
			}
			if next.TV == nil {
				next.TV = &TVPlayerPrefs{}
			}
			mergeTV(next.TV, &in)
		case "web":
			if isNull {
				next.Web = nil
				continue
			}
			var in WebPlayerPrefs
			if err := strictUnmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("%w: player.web: %v", ErrInvalidSetting, err)
			}
			if next.Web == nil {
				next.Web = &WebPlayerPrefs{}
			}
			if len(in.VideoCore) > 0 {
				next.Web.VideoCore = in.VideoCore
			}
			if len(in.MediaCore) > 0 {
				next.Web.MediaCore = in.MediaCore
			}
		default:
			return nil, fmt.Errorf("%w: unknown player section %q", ErrInvalidSetting, key)
		}
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	return next, nil
}

func strictUnmarshal(raw json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func mergeTV(dst, src *TVPlayerPrefs) {
	if src.PreferredAudioLanguages != nil {
		dst.PreferredAudioLanguages = src.PreferredAudioLanguages
	}
	if src.PreferredSubtitleLanguages != nil {
		dst.PreferredSubtitleLanguages = src.PreferredSubtitleLanguages
	}
	if src.IgnoredSubtitleLabels != nil {
		dst.IgnoredSubtitleLabels = src.IgnoredSubtitleLabels
	}
	if src.ShowSubtitles != nil {
		dst.ShowSubtitles = src.ShowSubtitles
	}
	if src.SubtitleFontSize != nil {
		dst.SubtitleFontSize = src.SubtitleFontSize
	}
	if src.AssRendering != nil {
		dst.AssRendering = src.AssRendering
	}
	if src.AutoPlay != nil {
		dst.AutoPlay = src.AutoPlay
	}
	if src.AutoNextEpisode != nil {
		dst.AutoNextEpisode = src.AutoNextEpisode
	}
	if src.SkipFillerEpisodes != nil {
		dst.SkipFillerEpisodes = src.SkipFillerEpisodes
	}
	if src.AutoSkipOpEd != nil {
		dst.AutoSkipOpEd = src.AutoSkipOpEd
	}
	if src.SkipDurationSec != nil {
		dst.SkipDurationSec = src.SkipDurationSec
	}
	if src.ButtonSeekSec != nil {
		dst.ButtonSeekSec = src.ButtonSeekSec
	}
	if src.DoubleTapSeekSec != nil {
		dst.DoubleTapSeekSec = src.DoubleTapSeekSec
	}
	if src.Speed != nil {
		dst.Speed = src.Speed
	}
	if src.LongPressFastForwardSpeed != nil {
		dst.LongPressFastForwardSpeed = src.LongPressFastForwardSpeed
	}
}

func (p *PlayerPrefs) clonePrefs() *PlayerPrefs {
	if p == nil {
		return &PlayerPrefs{}
	}
	data, _ := json.Marshal(p)
	var ret PlayerPrefs
	_ = json.Unmarshal(data, &ret)
	return &ret
}

// PlayerPrefs returns the profile's player preferences (empty if none).
func (s *Store) PlayerPrefs(ctx context.Context, profileID int64) (*PlayerPrefs, error) {
	st, err := s.Get(ctx, profileID)
	if err != nil {
		return nil, err
	}
	return ParsePlayerPrefs(st.Player)
}

// PatchPlayerPrefs applies a partial update to the profile's player
// preferences and returns the result. The other settings are kept.
func (s *Store) PatchPlayerPrefs(ctx context.Context, profileID int64, patch PlayerPrefsPatch) (*PlayerPrefs, error) {
	s.playerMu.Lock()
	defer s.playerMu.Unlock()
	st, err := s.Get(ctx, profileID)
	if err != nil {
		return nil, err
	}
	cur, err := ParsePlayerPrefs(st.Player)
	if err != nil {
		// Unreadable old data: start again rather than lock the profile out.
		cur = &PlayerPrefs{}
	}
	next, err := patch.Apply(cur)
	if err != nil {
		return nil, err
	}
	next.UpdatedAt = s.now().Unix()
	data, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	st.Player = data
	if err := s.Save(ctx, profileID, st); err != nil {
		return nil, err
	}
	return next, nil
}
