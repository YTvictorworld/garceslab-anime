// Package importer imports and exports profile data (plan §3.5): a public AniList
// list by user name (anonymous, anime only), the server's existing AniList account
// list (cached collection, no token use) and the profile JSON export format.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"seanime/internal/api/anilist"
	"seanime/internal/profiles"
	"seanime/internal/profiles/history"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/settings"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	// Format identifies the profile export JSON.
	Format = "garceslab-anime-profile"
	// Version is the export format version.
	Version = 1
	// MaxImportEntries caps an import.
	MaxImportEntries = 20000
)

var (
	ErrInvalidUsername = errors.New("PROFILE_IMPORT_INVALID_USERNAME")
	ErrListUnavailable = errors.New("PROFILE_IMPORT_LIST_UNAVAILABLE") // private or unknown user
	ErrInvalidFile     = errors.New("PROFILE_IMPORT_INVALID_FILE")
	ErrNothingToImport = errors.New("PROFILE_IMPORT_NOTHING_TO_IMPORT")
)

// AniList user names: 2-20 letters and digits.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,20}$`)

// AnimeCollectionFetcher is the anonymous AniList query used for public lists.
type AnimeCollectionFetcher interface {
	AnimeCollection(ctx context.Context, userName *string) (*anilist.AnimeCollection, error)
}

// ClientFetcher adapts an anilist.AnilistClient. It refuses authenticated clients.
type ClientFetcher struct{ Client anilist.AnilistClient }

func (f ClientFetcher) AnimeCollection(ctx context.Context, userName *string) (*anilist.AnimeCollection, error) {
	if f.Client == nil {
		return nil, errors.New("importer: no AniList client")
	}
	if f.Client.IsAuthenticated() {
		return nil, errors.New("importer: public list imports must use an anonymous AniList client")
	}
	// The wrapper refuses collection queries without a token; public lists don't need one.
	if impl, ok := f.Client.(*anilist.AnilistClientImpl); ok && impl.Client != nil {
		return impl.Client.AnimeCollection(ctx, userName)
	}
	return f.Client.AnimeCollection(ctx, userName)
}

// ImportPublicAnilist imports the public anime list of an AniList user.
func ImportPublicAnilist(ctx context.Context, p *localplatform.Platform, fetcher AnimeCollectionFetcher, username string, mode localplatform.ImportMode) (*localplatform.ProfileImportResult, error) {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return nil, ErrInvalidUsername
	}
	col, err := fetcher.AnimeCollection(ctx, &username)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrListUnavailable, err)
	}
	return ImportCollection(ctx, p, col, mode, "anilist:"+username)
}

// ImportCollection imports an AniList-shaped anime collection.
func ImportCollection(ctx context.Context, p *localplatform.Platform, col *anilist.AnimeCollection, mode localplatform.ImportMode, source string) (*localplatform.ProfileImportResult, error) {
	entries := localplatform.FromAnilistCollection(col)
	if len(entries) == 0 {
		return nil, ErrNothingToImport
	}
	if len(entries) > MaxImportEntries {
		return nil, fmt.Errorf("%w: too many entries", ErrInvalidFile)
	}
	return p.Import(ctx, entries, mode, source)
}

//----------------------------------------------------------------------------------------------------------------------
// JSON export / import

type ProfileExportInfo struct {
	Name          string  `json:"name"`
	AvatarID      string  `json:"avatarId"`
	Color         *string `json:"color,omitempty"`
	ScoreFormat   string  `json:"scoreFormat"`
	TitleLanguage string  `json:"titleLanguage"`
}

type ProfileExportCustomList struct {
	Name     string `json:"name"`
	MediaIDs []int  `json:"mediaIds"`
}

type ProfileExportFavourite struct {
	Kind     string `json:"kind"`
	RefID    int    `json:"refId"`
	Position int    `json:"position"`
}

type ProfileExportActivity struct {
	MediaType string  `json:"mediaType"`
	MediaID   int     `json:"mediaId"`
	Kind      string  `json:"kind"`
	Episode   *int    `json:"episode,omitempty"`
	OldValue  *string `json:"oldValue,omitempty"`
	NewValue  *string `json:"newValue,omitempty"`
	CreatedAt int64   `json:"createdAt"`
}

// ProfileExport is the profile export JSON (plan §3.5).
type ProfileExport struct {
	Format          string                             `json:"format"`
	Version         int                                `json:"version"`
	ExportedAt      int64                              `json:"exportedAt"`
	Profile         ProfileExportInfo                  `json:"profile"`
	Settings        *settings.ProfileSettings          `json:"settings"`
	Theme           json.RawMessage                    `json:"theme,omitempty"`
	Autoselect      json.RawMessage                    `json:"autoselect,omitempty"`
	Entries         []localplatform.ProfileImportEntry `json:"entries"`
	CustomLists     []ProfileExportCustomList          `json:"customLists"`
	Favourites      []ProfileExportFavourite           `json:"favourites"`
	WatchHistory    []history.WatchHistoryRow          `json:"watchHistory"`
	EpisodeProgress []history.EpisodeProgressRow       `json:"episodeProgress"`
	Activity        []ProfileExportActivity            `json:"activity"`
}

type rawValue struct {
	Value string `gorm:"column:value"`
}

func readRaw(ctx context.Context, db *gorm.DB, table string, profileID int64) json.RawMessage {
	var rows []rawValue
	_ = db.WithContext(ctx).Table(table).Select("value").Where("profile_id = ?", profileID).Limit(1).Find(&rows).Error
	if len(rows) == 0 || !json.Valid([]byte(rows[0].Value)) {
		return nil
	}
	return json.RawMessage(rows[0].Value)
}

// BuildExport collects everything a profile owns. Every query filters by profile_id.
func BuildExport(ctx context.Context, db *gorm.DB, p *localplatform.Platform, prof *profiles.Profile, now time.Time) (*ProfileExport, error) {
	if prof == nil || p == nil || p.ProfileID() != prof.ID {
		return nil, errors.New("importer: profile mismatch")
	}
	pid := prof.ID
	out := &ProfileExport{
		Format: Format, Version: Version, ExportedAt: now.Unix(),
		Profile: ProfileExportInfo{Name: prof.Name, AvatarID: prof.AvatarID, Color: prof.Color, ScoreFormat: prof.ScoreFormat, TitleLanguage: prof.TitleLanguage},
		Entries: []localplatform.ProfileImportEntry{}, CustomLists: []ProfileExportCustomList{}, Favourites: []ProfileExportFavourite{},
		WatchHistory: []history.WatchHistoryRow{}, EpisodeProgress: []history.EpisodeProgressRow{}, Activity: []ProfileExportActivity{},
	}

	st, err := settings.NewStore(db).Get(ctx, pid)
	if err != nil {
		return nil, err
	}
	out.Settings = st
	out.Theme = readRaw(ctx, db, "profile_theme", pid)
	out.Autoselect = readRaw(ctx, db, "profile_autoselect", pid)

	lists, err := p.CustomLists(ctx)
	if err != nil {
		return nil, err
	}
	membership := map[int][]string{}
	for _, l := range lists {
		out.CustomLists = append(out.CustomLists, ProfileExportCustomList{Name: l.Name, MediaIDs: l.MediaIDs})
		for _, mid := range l.MediaIDs {
			membership[mid] = append(membership[mid], l.Name)
		}
	}

	rows, err := p.Entries(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Entries = append(out.Entries, localplatform.ProfileImportEntry{
			MediaID: r.MediaID, Status: r.Status, Progress: r.Progress, ScoreRaw: r.ScoreRaw, Repeat: r.Repeat,
			Private: r.Private, HiddenFromStatusLists: r.HiddenFromStatusLists, Notes: r.Notes,
			StartedAt: r.StartedAt(), CompletedAt: r.CompletedAt(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			CustomLists: membership[r.MediaID],
		})
	}

	if err := db.WithContext(ctx).Table("favourites").Select("kind, ref_id, position").Where("profile_id = ?", pid).
		Order("kind, position").Scan(&out.Favourites).Error; err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Where("profile_id = ?", pid).Order("time_updated DESC").Find(&out.WatchHistory).Error; err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Where("profile_id = ?", pid).Order("media_id, episode_number").Find(&out.EpisodeProgress).Error; err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Table("activity_log").Select("media_type, media_id, kind, episode, old_value, new_value, created_at").
		Where("profile_id = ?", pid).Order("created_at, id").Scan(&out.Activity).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// ParseJSON reads a profile export, or an AniList-shaped AnimeCollection (e.g. a
// simulated platform collection), and returns the entries to import plus the
// optional continuity data of an export.
func ParseJSON(data []byte) (entries []localplatform.ProfileImportEntry, exp *ProfileExport, err error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidFile, err)
	}
	if _, ok := probe["MediaListCollection"]; ok {
		var col anilist.AnimeCollection
		if err := json.Unmarshal(data, &col); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidFile, err)
		}
		return localplatform.FromAnilistCollection(&col), nil, nil
	}
	var e ProfileExport
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidFile, err)
	}
	if e.Format != Format {
		return nil, nil, fmt.Errorf("%w: unknown format", ErrInvalidFile)
	}
	if e.Version > Version {
		return nil, nil, fmt.Errorf("%w: export version %d is newer than this server (%d)", ErrInvalidFile, e.Version, Version)
	}
	// Custom list membership may come from either side of the export.
	byMedia := map[int]int{}
	for i, en := range e.Entries {
		byMedia[en.MediaID] = i
	}
	for _, l := range e.CustomLists {
		for _, mid := range l.MediaIDs {
			if i, ok := byMedia[mid]; ok && !contains(e.Entries[i].CustomLists, l.Name) {
				e.Entries[i].CustomLists = append(e.Entries[i].CustomLists, l.Name)
			}
		}
	}
	return e.Entries, &e, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// ImportJSON imports a JSON file into the profile: list entries (and custom
// lists), plus watch history and episode progress when the file is an export.
func ImportJSON(ctx context.Context, db *gorm.DB, p *localplatform.Platform, data []byte, mode localplatform.ImportMode) (*localplatform.ProfileImportResult, error) {
	entries, exp, err := ParseJSON(data)
	if err != nil {
		return nil, err
	}
	if len(entries) > MaxImportEntries {
		return nil, fmt.Errorf("%w: too many entries", ErrInvalidFile)
	}
	if len(entries) == 0 && (exp == nil || len(exp.WatchHistory) == 0) {
		return nil, ErrNothingToImport
	}
	res := &localplatform.ProfileImportResult{}
	if len(entries) > 0 {
		res, err = p.Import(ctx, entries, mode, "json")
		if err != nil {
			return nil, err
		}
	}
	if exp != nil {
		if err := importContinuity(ctx, db, p.ProfileID(), exp, mode); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func importContinuity(ctx context.Context, db *gorm.DB, pid int64, exp *ProfileExport, mode localplatform.ImportMode) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if mode == localplatform.ImportReplace {
			if err := tx.Exec("DELETE FROM watch_history WHERE profile_id = ?", pid).Error; err != nil {
				return err
			}
			if err := tx.Exec("DELETE FROM episode_progress WHERE profile_id = ?", pid).Error; err != nil {
				return err
			}
		}
		for _, r := range exp.WatchHistory {
			if r.MediaID <= 0 {
				continue
			}
			if err := tx.Exec(`INSERT INTO watch_history (profile_id, media_id, episode_number, kind, filepath, position_sec, duration, time_added, time_updated)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(profile_id, media_id) DO UPDATE SET episode_number = excluded.episode_number, kind = excluded.kind, filepath = excluded.filepath,
  position_sec = excluded.position_sec, duration = excluded.duration, time_updated = excluded.time_updated
WHERE excluded.time_updated > watch_history.time_updated`,
				pid, r.MediaID, r.EpisodeNumber, r.Kind, r.Filepath, r.PositionSec, r.Duration, r.TimeAdded, r.TimeUpdated).Error; err != nil {
				return err
			}
		}
		for _, r := range exp.EpisodeProgress {
			if r.MediaID <= 0 {
				continue
			}
			if err := tx.Exec(`INSERT INTO episode_progress (profile_id, media_id, episode_number, position_sec, duration, completed, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(profile_id, media_id, episode_number) DO UPDATE SET position_sec = excluded.position_sec, duration = excluded.duration,
  completed = excluded.completed, updated_at = excluded.updated_at
WHERE excluded.updated_at > episode_progress.updated_at`,
				pid, r.MediaID, r.EpisodeNumber, r.PositionSec, r.Duration, r.Completed, r.UpdatedAt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
