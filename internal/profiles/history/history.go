// Package history stores the watch history ("continue watching", resume position)
// per profile in profiles.db: watch_history (one row per media, the last episode)
// and episode_progress (one row per episode). It implements continuity.HistoryStore.
package history

import (
	"context"
	"fmt"
	"seanime/internal/continuity"
	"seanime/internal/profiles"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CompletedRatio marks an episode as completed in episode_progress.
const CompletedRatio = continuity.IgnoreRatioThreshold

// ErrNoProfile is returned by writes without a profile in the context.
var ErrNoProfile = fmt.Errorf("%w: watch history not saved", profiles.ErrProfileRequired)

// WatchHistoryRow is a row of watch_history.
type WatchHistoryRow struct {
	ProfileID     int64   `gorm:"column:profile_id;primaryKey" json:"-"`
	MediaID       int     `gorm:"column:media_id;primaryKey" json:"mediaId"`
	EpisodeNumber int     `gorm:"column:episode_number" json:"episodeNumber"`
	Kind          string  `gorm:"column:kind" json:"kind"`
	Filepath      *string `gorm:"column:filepath" json:"filepath,omitempty"`
	PositionSec   float64 `gorm:"column:position_sec" json:"positionSec"`
	Duration      float64 `gorm:"column:duration" json:"duration"`
	TimeAdded     int64   `gorm:"column:time_added" json:"timeAdded"`
	TimeUpdated   int64   `gorm:"column:time_updated" json:"timeUpdated"`
}

func (WatchHistoryRow) TableName() string { return "watch_history" }

// EpisodeProgressRow is a row of episode_progress.
type EpisodeProgressRow struct {
	ProfileID     int64   `gorm:"column:profile_id;primaryKey" json:"-"`
	MediaID       int     `gorm:"column:media_id;primaryKey" json:"mediaId"`
	EpisodeNumber int     `gorm:"column:episode_number;primaryKey" json:"episodeNumber"`
	PositionSec   float64 `gorm:"column:position_sec" json:"positionSec"`
	Duration      float64 `gorm:"column:duration" json:"duration"`
	Completed     bool    `gorm:"column:completed" json:"completed"`
	UpdatedAt     int64   `gorm:"column:updated_at" json:"updatedAt"`
}

func (EpisodeProgressRow) TableName() string { return "episode_progress" }

type Store struct {
	db  *gorm.DB
	now func() time.Time
}

var _ continuity.HistoryStore = (*Store)(nil)

func New(db *gorm.DB) *Store { return &Store{db: db, now: time.Now} }

func toItem(r *WatchHistoryRow) *continuity.WatchHistoryItem {
	fp := ""
	if r.Filepath != nil {
		fp = *r.Filepath
	}
	return &continuity.WatchHistoryItem{
		Kind:          continuity.Kind(r.Kind),
		Filepath:      fp,
		MediaId:       r.MediaID,
		EpisodeNumber: r.EpisodeNumber,
		CurrentTime:   r.PositionSec,
		Duration:      r.Duration,
		TimeAdded:     time.Unix(r.TimeAdded, 0),
		TimeUpdated:   time.Unix(r.TimeUpdated, 0),
	}
}

func (s *Store) All(ctx context.Context) (map[int]*continuity.WatchHistoryItem, error) {
	ret := map[int]*continuity.WatchHistoryItem{}
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return ret, nil
	}
	var rows []WatchHistoryRow
	if err := s.db.WithContext(ctx).Where("profile_id = ?", pid).Order("time_updated DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		ret[rows[i].MediaID] = toItem(&rows[i])
	}
	return ret, nil
}

func (s *Store) Get(ctx context.Context, mediaId int) (*continuity.WatchHistoryItem, bool, error) {
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return nil, false, nil
	}
	var rows []WatchHistoryRow
	if err := s.db.WithContext(ctx).Where("profile_id = ? AND media_id = ?", pid, mediaId).Limit(1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return toItem(&rows[0]), true, nil
}

func (s *Store) Set(ctx context.Context, item *continuity.WatchHistoryItem) error {
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return ErrNoProfile
	}
	if item == nil || item.MediaId <= 0 {
		return fmt.Errorf("history: invalid item")
	}
	added, updated := item.TimeAdded, item.TimeUpdated
	if added.IsZero() {
		added = s.now()
	}
	if updated.IsZero() {
		updated = s.now()
	}
	var fp *string
	if item.Filepath != "" {
		fp = &item.Filepath
	}
	row := WatchHistoryRow{ProfileID: pid, MediaID: item.MediaId, EpisodeNumber: item.EpisodeNumber, Kind: string(item.Kind), Filepath: fp,
		PositionSec: item.CurrentTime, Duration: item.Duration, TimeAdded: added.Unix(), TimeUpdated: updated.Unix()}
	completed := item.Duration > 0 && item.CurrentTime/item.Duration >= CompletedRatio
	ep := EpisodeProgressRow{ProfileID: pid, MediaID: item.MediaId, EpisodeNumber: item.EpisodeNumber,
		PositionSec: item.CurrentTime, Duration: item.Duration, Completed: completed, UpdatedAt: updated.Unix()}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "profile_id"}, {Name: "media_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"episode_number", "kind", "filepath", "position_sec", "duration", "time_updated"}),
		}).Create(&row).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "profile_id"}, {Name: "media_id"}, {Name: "episode_number"}},
			DoUpdates: clause.AssignmentColumns([]string{"position_sec", "duration", "completed", "updated_at"}),
		}).Create(&ep).Error
	})
}

func (s *Store) Delete(ctx context.Context, mediaId int) error {
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return ErrNoProfile
	}
	return s.db.WithContext(ctx).Where("profile_id = ? AND media_id = ?", pid, mediaId).Delete(&WatchHistoryRow{}).Error
}

func (s *Store) Trim(ctx context.Context, max int) error {
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return nil
	}
	return s.db.WithContext(ctx).Exec(`DELETE FROM watch_history WHERE profile_id = ? AND media_id NOT IN (
  SELECT media_id FROM watch_history WHERE profile_id = ? ORDER BY time_updated DESC, media_id DESC LIMIT ?)`, pid, pid, max).Error
}

// EpisodeProgress returns the per-episode positions of a media for the profile in ctx.
func (s *Store) EpisodeProgress(ctx context.Context, mediaID int) ([]EpisodeProgressRow, error) {
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return []EpisodeProgressRow{}, nil
	}
	var rows []EpisodeProgressRow
	err := s.db.WithContext(ctx).Where("profile_id = ? AND media_id = ?", pid, mediaID).Order("episode_number").Find(&rows).Error
	return rows, err
}
