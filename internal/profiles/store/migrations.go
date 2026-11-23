package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// migration is one versioned schema step. Migrations are append-only: never edit
// a released one, add a new version instead.
type migration struct {
	version    int
	name       string
	statements []string
}

var migrations = []migration{
	{version: 1, name: "initial schema", statements: schemaV1},
}

// LatestVersion is the schema version this build expects.
func LatestVersion() int { return migrations[len(migrations)-1].version }

func migrate(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).Exec(`CREATE TABLE IF NOT EXISTS profiles_schema_version (
  version    INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  applied_at INTEGER NOT NULL
)`).Error; err != nil {
		return fmt.Errorf("profiles store: create schema version table: %w", err)
	}

	current, err := currentVersion(db)
	if err != nil {
		return err
	}
	if current > LatestVersion() {
		return fmt.Errorf("profiles store: database schema version %d is newer than this build (%d)", current, LatestVersion())
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, stmt := range m.statements {
				if err := tx.Exec(stmt).Error; err != nil {
					return fmt.Errorf("%w\n%s", err, stmt)
				}
			}
			return tx.Exec("INSERT INTO profiles_schema_version (version, name, applied_at) VALUES (?, ?, ?)",
				m.version, m.name, time.Now().Unix()).Error
		})
		if err != nil {
			return fmt.Errorf("profiles store: migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func currentVersion(db *gorm.DB) (int, error) {
	var v int
	if err := db.Raw("SELECT COALESCE(MAX(version), 0) FROM profiles_schema_version").Scan(&v).Error; err != nil {
		return 0, fmt.Errorf("profiles store: read schema version: %w", err)
	}
	return v, nil
}

// schemaV1 is the full schema from the plan (§3.1), with the confirmed changes:
// no default/remembered profile, nullable argon2id PIN, soft delete, plus
// profile_audit (§3.3) and profiles_meta for server-side secrets.
var schemaV1 = []string{
	`CREATE TABLE profiles_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
)`,

	`CREATE TABLE profiles (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  name             TEXT    NOT NULL COLLATE NOCASE UNIQUE,
  avatar_id        TEXT    NOT NULL,
  color            TEXT,
  pin_hash         TEXT,
  pin_failed       INTEGER NOT NULL DEFAULT 0,
  pin_locked_until INTEGER,
  token_version    INTEGER NOT NULL DEFAULT 1,
  sort_order       INTEGER NOT NULL DEFAULT 0,
  score_format     TEXT    NOT NULL DEFAULT 'POINT_10_DECIMAL'
                   CHECK (score_format IN ('POINT_100','POINT_10_DECIMAL','POINT_10','POINT_5','POINT_3')),
  title_language   TEXT    NOT NULL DEFAULT 'ROMAJI',
  deleted_at       INTEGER,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  last_used_at     INTEGER
)`,

	// Current device <-> profile session (event routing and lanes). Never used to
	// remember a profile across app launches.
	`CREATE TABLE profile_devices (
  client_id    TEXT    NOT NULL PRIMARY KEY,
  profile_id   INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  platform     TEXT,
  last_seen_at INTEGER NOT NULL
)`,
	`CREATE INDEX ix_profile_devices_profile ON profile_devices(profile_id)`,

	`CREATE TABLE list_entries (
  id                       INTEGER PRIMARY KEY AUTOINCREMENT,
  profile_id               INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  media_type               TEXT    NOT NULL CHECK (media_type IN ('ANIME','MANGA')),
  media_id                 INTEGER NOT NULL,
  status                   TEXT    NOT NULL CHECK (status IN ('CURRENT','PLANNING','COMPLETED','DROPPED','PAUSED','REPEATING')),
  progress                 INTEGER NOT NULL DEFAULT 0 CHECK (progress >= 0),
  progress_volumes         INTEGER CHECK (progress_volumes >= 0),
  score_raw                INTEGER CHECK (score_raw BETWEEN 0 AND 100),
  repeat                   INTEGER NOT NULL DEFAULT 0 CHECK (repeat >= 0),
  private                  INTEGER NOT NULL DEFAULT 0,
  hidden_from_status_lists INTEGER NOT NULL DEFAULT 0,
  notes                    TEXT,
  started_y INTEGER, started_m INTEGER, started_d INTEGER,
  completed_y INTEGER, completed_m INTEGER, completed_d INTEGER,
  created_at               INTEGER NOT NULL,
  updated_at               INTEGER NOT NULL,
  UNIQUE (profile_id, media_id)
)`,
	`CREATE INDEX ix_entries_profile_status ON list_entries(profile_id, media_type, status, updated_at DESC)`,
	`CREATE INDEX ix_entries_media ON list_entries(media_id)`,

	`CREATE TABLE favourites (
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  kind       TEXT    NOT NULL CHECK (kind IN ('ANIME','MANGA','CHARACTER','STAFF','STUDIO')),
  ref_id     INTEGER NOT NULL,
  position   INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (profile_id, kind, ref_id)
)`,

	`CREATE TABLE custom_lists (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  media_type TEXT    NOT NULL CHECK (media_type IN ('ANIME','MANGA')),
  name       TEXT    NOT NULL COLLATE NOCASE,
  position   INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  UNIQUE (profile_id, media_type, name)
)`,
	`CREATE TABLE custom_list_items (
  list_id  INTEGER NOT NULL REFERENCES custom_lists(id) ON DELETE CASCADE,
  entry_id INTEGER NOT NULL REFERENCES list_entries(id) ON DELETE CASCADE,
  added_at INTEGER NOT NULL,
  PRIMARY KEY (list_id, entry_id)
)`,
	`CREATE INDEX ix_custom_list_items_entry ON custom_list_items(entry_id)`,

	`CREATE TABLE watch_history (
  profile_id     INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  media_id       INTEGER NOT NULL,
  episode_number INTEGER NOT NULL,
  kind           TEXT    NOT NULL,
  filepath       TEXT,
  position_sec   REAL    NOT NULL, -- "current_time" in the plan; renamed (SQLite keyword)
  duration       REAL    NOT NULL,
  time_added     INTEGER NOT NULL,
  time_updated   INTEGER NOT NULL,
  PRIMARY KEY (profile_id, media_id)
)`,
	`CREATE INDEX ix_watch_history_recent ON watch_history(profile_id, time_updated DESC)`,

	`CREATE TABLE episode_progress (
  profile_id     INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  media_id       INTEGER NOT NULL,
  episode_number INTEGER NOT NULL,
  position_sec   REAL    NOT NULL, -- "current_time" in the plan; renamed (SQLite keyword)
  duration       REAL    NOT NULL,
  completed      INTEGER NOT NULL DEFAULT 0,
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (profile_id, media_id, episode_number)
)`,

	`CREATE TABLE activity_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  media_type TEXT    NOT NULL,
  media_id   INTEGER NOT NULL,
  kind       TEXT    NOT NULL CHECK (kind IN ('added','status','progress','score','repeat','started','completed','deleted','imported')),
  episode    INTEGER,
  old_value  TEXT,
  new_value  TEXT,
  created_at INTEGER NOT NULL
)`,
	`CREATE INDEX ix_activity_profile_time ON activity_log(profile_id, created_at DESC)`,

	`CREATE TABLE profile_settings (
  profile_id INTEGER PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
  version    INTEGER NOT NULL DEFAULT 1,
  value      TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
)`,
	`CREATE TABLE profile_theme (
  profile_id INTEGER PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
  value      TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
)`,
	`CREATE TABLE profile_autoselect (
  profile_id INTEGER PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
  value      TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
)`,

	// Global metadata, no profile_id.
	`CREATE TABLE media_snapshots (
  media_id     INTEGER PRIMARY KEY,
  media_type   TEXT    NOT NULL CHECK (media_type IN ('ANIME','MANGA')),
  media_status TEXT,
  payload      TEXT    NOT NULL,
  fetched_at   INTEGER NOT NULL
)`,
	`CREATE INDEX ix_snapshots_refresh ON media_snapshots(media_status, fetched_at)`,

	// Who did what to which profile (§3.3). The target has no FK so the row
	// survives the purge of the profile; the actor is kept as NULL if purged.
	`CREATE TABLE profile_audit (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  actor_profile_id  INTEGER REFERENCES profiles(id) ON DELETE SET NULL,
  target_profile_id INTEGER NOT NULL,
  target_name       TEXT    NOT NULL,
  action            TEXT    NOT NULL CHECK (action IN ('create','update','delete','restore','purge','pin_set','pin_change','pin_remove')),
  created_at        INTEGER NOT NULL
)`,
	`CREATE INDEX ix_profile_audit_time ON profile_audit(created_at DESC)`,
}
