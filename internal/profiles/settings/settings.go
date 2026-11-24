// Package settings holds the per-profile settings (plan §3.7.3): a whitelist of
// overrides of the global settings (profile_settings), the theme (profile_theme)
// and the torrent auto-select profile (profile_autoselect). GET /settings returns
// the effective settings (global + the profile's overrides); PATCH routes each
// field to the profile or to the global settings.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"seanime/internal/database/models"
	"seanime/internal/library/anime"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// CurrentVersion is profile_settings.version.
const CurrentVersion = 1

// MaxPlayerPrefsBytes caps the free-form player preferences.
const MaxPlayerPrefsBytes = 16 << 10

var ErrInvalidSetting = errors.New("PROFILE_INVALID_SETTING")

// Library overrides (nil = use the global value).
type ProfileLibraryOverrides struct {
	AutoUpdateProgress    *bool   `json:"autoUpdateProgress,omitempty"`
	AutoPlayNextEpisode   *bool   `json:"autoPlayNextEpisode,omitempty"`
	EnableWatchContinuity *bool   `json:"enableWatchContinuity,omitempty"`
	DefaultPlaybackSource *string `json:"defaultPlaybackSource,omitempty"`
}

// Anilist overrides (display preferences; AniList itself is anonymous).
type ProfileAnilistOverrides struct {
	HideAudienceScore  *bool `json:"hideAudienceScore,omitempty"`
	EnableAdultContent *bool `json:"enableAdultContent,omitempty"`
	BlurAdultContent   *bool `json:"blurAdultContent,omitempty"`
}

// ProfileSettings is the JSON stored in profile_settings.value.
type ProfileSettings struct {
	Library *ProfileLibraryOverrides `json:"library,omitempty"`
	Anilist *ProfileAnilistOverrides `json:"anilist,omitempty"`
	// MarkWatchingOnStart replaces the auto-watching plugin (default on).
	MarkWatchingOnStart *bool `json:"markWatchingOnStart,omitempty"`
	// CompletedAction is the status for a COMPLETED anime when playback starts:
	// "REPEATING" (default) or "CURRENT".
	CompletedAction *string `json:"completedAction,omitempty"`
	// Player is the player preferences per client (PlayerPrefs, player.go).
	Player json.RawMessage `json:"player,omitempty"`
}

// ProfileFields are the settings paths (models.SetSettingsPath syntax) stored per profile.
var ProfileFields = []string{
	"library.autoUpdateProgress",
	"library.autoPlayNextEpisode",
	"library.enableWatchContinuity",
	"library.defaultPlaybackSource",
	"anilist.hideAudienceScore",
	"anilist.enableAdultContent",
	"anilist.blurAdultContent",
}

// IsProfileField reports whether a settings path is stored per profile.
func IsProfileField(path string) bool {
	path = strings.TrimSpace(path)
	for _, f := range ProfileFields {
		if strings.EqualFold(f, path) {
			return true
		}
	}
	return false
}

// MarkWatching returns the effective auto-watching settings.
func (s *ProfileSettings) MarkWatching() (enabled bool, completedAction string) {
	enabled, completedAction = true, "REPEATING"
	if s == nil {
		return
	}
	if s.MarkWatchingOnStart != nil {
		enabled = *s.MarkWatchingOnStart
	}
	if s.CompletedAction != nil && *s.CompletedAction == "CURRENT" {
		completedAction = "CURRENT"
	}
	return
}

// Validate checks the values.
func (s *ProfileSettings) Validate() error {
	if s == nil {
		return nil
	}
	if s.CompletedAction != nil && *s.CompletedAction != "REPEATING" && *s.CompletedAction != "CURRENT" {
		return fmt.Errorf("%w: completedAction must be REPEATING or CURRENT", ErrInvalidSetting)
	}
	if len(s.Player) > 0 {
		if len(s.Player) > MaxPlayerPrefsBytes {
			return fmt.Errorf("%w: player preferences are too large", ErrInvalidSetting)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(s.Player, &obj); err != nil {
			return fmt.Errorf("%w: player preferences must be a JSON object", ErrInvalidSetting)
		}
		// Phase 4: typed per-client sections (player.go).
		prefs, err := ParsePlayerPrefs(s.Player)
		if err != nil {
			return err
		}
		if err := prefs.Validate(); err != nil {
			return err
		}
	}
	if s.Library != nil && s.Library.DefaultPlaybackSource != nil && len(*s.Library.DefaultPlaybackSource) > 200 {
		return fmt.Errorf("%w: defaultPlaybackSource is too long", ErrInvalidSetting)
	}
	return nil
}

// Effective returns a copy of global with the profile's overrides applied.
func Effective(global *models.Settings, p *ProfileSettings) *models.Settings {
	if global == nil {
		return nil
	}
	data, _ := json.Marshal(global)
	var ret models.Settings
	_ = json.Unmarshal(data, &ret)
	ret.ID = global.ID
	ret.CreatedAt, ret.UpdatedAt = global.CreatedAt, global.UpdatedAt
	if p == nil {
		return &ret
	}
	if l := p.Library; l != nil && ret.Library != nil {
		setBool(&ret.Library.AutoUpdateProgress, l.AutoUpdateProgress)
		setBool(&ret.Library.AutoPlayNextEpisode, l.AutoPlayNextEpisode)
		setBool(&ret.Library.EnableWatchContinuity, l.EnableWatchContinuity)
		if l.DefaultPlaybackSource != nil {
			ret.Library.DefaultPlaybackSource = *l.DefaultPlaybackSource
		}
	}
	if a := p.Anilist; a != nil && ret.Anilist != nil {
		setBool(&ret.Anilist.HideAudienceScore, a.HideAudienceScore)
		setBool(&ret.Anilist.EnableAdultContent, a.EnableAdultContent)
		setBool(&ret.Anilist.BlurAdultContent, a.BlurAdultContent)
	}
	return &ret
}

func setBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

// Split takes the per-profile fields out of next (a full settings body saved by a
// profile): it returns them as overrides on top of current and puts the previous
// global values back into next, so a profile never changes another's defaults.
func Split(prevGlobal, next *models.Settings, current *ProfileSettings) *ProfileSettings {
	ret := current.clone()
	if next == nil {
		return ret
	}
	if next.Library != nil {
		if ret.Library == nil {
			ret.Library = &ProfileLibraryOverrides{}
		}
		ret.Library.AutoUpdateProgress = new(next.Library.AutoUpdateProgress)
		ret.Library.AutoPlayNextEpisode = new(next.Library.AutoPlayNextEpisode)
		ret.Library.EnableWatchContinuity = new(next.Library.EnableWatchContinuity)
		ret.Library.DefaultPlaybackSource = new(next.Library.DefaultPlaybackSource)
		if prevGlobal != nil && prevGlobal.Library != nil {
			next.Library.AutoUpdateProgress = prevGlobal.Library.AutoUpdateProgress
			next.Library.AutoPlayNextEpisode = prevGlobal.Library.AutoPlayNextEpisode
			next.Library.EnableWatchContinuity = prevGlobal.Library.EnableWatchContinuity
			next.Library.DefaultPlaybackSource = prevGlobal.Library.DefaultPlaybackSource
		}
	}
	if next.Anilist != nil {
		if ret.Anilist == nil {
			ret.Anilist = &ProfileAnilistOverrides{}
		}
		ret.Anilist.HideAudienceScore = new(next.Anilist.HideAudienceScore)
		ret.Anilist.EnableAdultContent = new(next.Anilist.EnableAdultContent)
		ret.Anilist.BlurAdultContent = new(next.Anilist.BlurAdultContent)
		if prevGlobal != nil && prevGlobal.Anilist != nil {
			next.Anilist.HideAudienceScore = prevGlobal.Anilist.HideAudienceScore
			next.Anilist.EnableAdultContent = prevGlobal.Anilist.EnableAdultContent
			next.Anilist.BlurAdultContent = prevGlobal.Anilist.BlurAdultContent
		}
	}
	return ret
}

// SetPath sets one per-profile field from a settings path and a JSON value.
func (s *ProfileSettings) SetPath(path string, value any) error {
	b, isBool := value.(bool)
	str, isStr := value.(string)
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "library.autoupdateprogress", "library.autoplaynextepisode", "library.enablewatchcontinuity",
		"anilist.hideaudiencescore", "anilist.enableadultcontent", "anilist.bluradultcontent":
		if !isBool {
			return fmt.Errorf("%w: %s must be a boolean", ErrInvalidSetting, path)
		}
	case "library.defaultplaybacksource":
		if !isStr {
			return fmt.Errorf("%w: %s must be a string", ErrInvalidSetting, path)
		}
	default:
		return fmt.Errorf("%w: %s is not a profile setting", ErrInvalidSetting, path)
	}
	if s.Library == nil {
		s.Library = &ProfileLibraryOverrides{}
	}
	if s.Anilist == nil {
		s.Anilist = &ProfileAnilistOverrides{}
	}
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "library.autoupdateprogress":
		s.Library.AutoUpdateProgress = &b
	case "library.autoplaynextepisode":
		s.Library.AutoPlayNextEpisode = &b
	case "library.enablewatchcontinuity":
		s.Library.EnableWatchContinuity = &b
	case "library.defaultplaybacksource":
		s.Library.DefaultPlaybackSource = &str
	case "anilist.hideaudiencescore":
		s.Anilist.HideAudienceScore = &b
	case "anilist.enableadultcontent":
		s.Anilist.EnableAdultContent = &b
	case "anilist.bluradultcontent":
		s.Anilist.BlurAdultContent = &b
	}
	return nil
}

func (s *ProfileSettings) clone() *ProfileSettings {
	if s == nil {
		return &ProfileSettings{}
	}
	data, _ := json.Marshal(s)
	var ret ProfileSettings
	_ = json.Unmarshal(data, &ret)
	return &ret
}

//----------------------------------------------------------------------------------------------------------------------
// Store

type valueRow struct {
	ProfileID int64  `gorm:"column:profile_id;primaryKey"`
	Version   int    `gorm:"column:version"`
	Value     string `gorm:"column:value"`
	UpdatedAt int64  `gorm:"column:updated_at"`
}

type Store struct {
	db  *gorm.DB
	now func() time.Time

	mu    sync.RWMutex
	cache map[int64]*ProfileSettings

	// playerMu serialises player preference patches (read-modify-write).
	playerMu sync.Mutex
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db, now: time.Now, cache: map[int64]*ProfileSettings{}}
}

func (s *Store) read(ctx context.Context, table string, profileID int64, out any) (bool, error) {
	var rows []valueRow
	if err := s.db.WithContext(ctx).Table(table).Select("profile_id, value, updated_at").
		Where("profile_id = ?", profileID).Limit(1).Find(&rows).Error; err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	if err := json.Unmarshal([]byte(rows[0].Value), out); err != nil {
		return false, fmt.Errorf("profiles settings: corrupt %s for profile %d: %w", table, profileID, err)
	}
	return true, nil
}

func (s *Store) write(ctx context.Context, table string, profileID int64, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := s.now().Unix()
	if table == "profile_settings" {
		return s.db.WithContext(ctx).Exec(`INSERT INTO profile_settings (profile_id, version, value, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(profile_id) DO UPDATE SET version = excluded.version, value = excluded.value, updated_at = excluded.updated_at`,
			profileID, CurrentVersion, string(data), now).Error
	}
	return s.db.WithContext(ctx).Exec(`INSERT INTO `+table+` (profile_id, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(profile_id) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, profileID, string(data), now).Error
}

// Get returns a copy of the profile's settings (empty overrides if none).
func (s *Store) Get(ctx context.Context, profileID int64) (*ProfileSettings, error) {
	s.mu.RLock()
	cached, ok := s.cache[profileID]
	s.mu.RUnlock()
	if ok {
		return cached.clone(), nil
	}
	ret := &ProfileSettings{}
	if _, err := s.read(ctx, "profile_settings", profileID, ret); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[profileID] = ret.clone()
	s.mu.Unlock()
	return ret, nil
}

// Save validates and stores the profile's settings.
func (s *Store) Save(ctx context.Context, profileID int64, v *ProfileSettings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	err := s.write(ctx, "profile_settings", profileID, v)
	s.mu.Lock()
	delete(s.cache, profileID)
	s.mu.Unlock()
	return err
}

// Forget drops the cached settings of a profile (purged).
func (s *Store) Forget(profileID int64) {
	s.mu.Lock()
	delete(s.cache, profileID)
	s.mu.Unlock()
}

// Theme returns the profile's theme (found=false: use the global one).
func (s *Store) Theme(ctx context.Context, profileID int64) (*models.Theme, bool, error) {
	var t models.Theme
	found, err := s.read(ctx, "profile_theme", profileID, &t)
	if err != nil || !found {
		return nil, false, err
	}
	return &t, true, nil
}

func (s *Store) SaveTheme(ctx context.Context, profileID int64, t *models.Theme) error {
	return s.write(ctx, "profile_theme", profileID, t)
}

// AutoSelect returns the profile's torrent auto-select profile (found=false: none).
func (s *Store) AutoSelect(ctx context.Context, profileID int64) (*anime.AutoSelectProfile, bool, error) {
	var p anime.AutoSelectProfile
	found, err := s.read(ctx, "profile_autoselect", profileID, &p)
	if err != nil || !found {
		return nil, false, err
	}
	p.DbID = 1 // "exists" for db_bridge.FindAutoSelectProfile
	return &p, true, nil
}

func (s *Store) SaveAutoSelect(ctx context.Context, profileID int64, p *anime.AutoSelectProfile) error {
	if p == nil {
		return fmt.Errorf("%w: empty auto-select profile", ErrInvalidSetting)
	}
	return s.write(ctx, "profile_autoselect", profileID, p)
}

func (s *Store) DeleteAutoSelect(ctx context.Context, profileID int64) error {
	return s.db.WithContext(ctx).Exec("DELETE FROM profile_autoselect WHERE profile_id = ?", profileID).Error
}
