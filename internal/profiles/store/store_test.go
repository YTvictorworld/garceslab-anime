package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenEnforcesPragmasAndMigrates(t *testing.T) {
	s := openTest(t)
	db := s.DB()

	// Check on several connections: pragmas are per connection.
	for i := 0; i < 4; i++ {
		var fk int
		require.NoError(t, db.Raw("PRAGMA foreign_keys").Scan(&fk).Error)
		assert.Equal(t, 1, fk)
	}
	var mode string
	require.NoError(t, db.Raw("PRAGMA journal_mode").Scan(&mode).Error)
	assert.Equal(t, "wal", mode)

	v, err := s.SchemaVersion()
	require.NoError(t, err)
	assert.Equal(t, LatestVersion(), v)

	for _, table := range []string{"profiles", "profile_devices", "list_entries", "favourites", "custom_lists",
		"custom_list_items", "watch_history", "episode_progress", "activity_log", "profile_settings",
		"profile_theme", "profile_autoselect", "media_snapshots", "profile_audit", "profiles_meta"} {
		var n int
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n).Error)
		assert.Equal(t, 1, n, table)
	}

	// No default/remember columns (decision 2026-11-23).
	cols := columns(t, db, "profiles")
	assert.NotContains(t, cols, "is_default")
	assert.NotContains(t, cols, "remember")
	assert.Contains(t, cols, "deleted_at")
}

func TestReopenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	require.NoError(t, err)
	require.NoError(t, s1.Close())
	s2, err := Open(dir)
	require.NoError(t, err)
	defer s2.Close()
	var n int
	require.NoError(t, s2.DB().Raw("SELECT COUNT(*) FROM profiles_schema_version").Scan(&n).Error)
	assert.Equal(t, len(migrations), n)
}

func TestForeignKeyCheckFailsWhenDisabled(t *testing.T) {
	// The mattn-style DSN used by seanime.db is ignored by this driver: FKs stay off.
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "x.db")+"?_foreign_keys=on"), &gorm.Config{})
	require.NoError(t, err)
	s := &Store{db: gdb}
	defer s.Close()
	assert.ErrorIs(t, s.checkForeignKeys(), ErrForeignKeysDisabled)
}

func TestInMemory(t *testing.T) {
	s, err := Open("")
	require.NoError(t, err)
	defer s.Close()
	v, err := s.SchemaVersion()
	require.NoError(t, err)
	assert.Equal(t, LatestVersion(), v)
}

// Every table with a profile_id column must reference profiles(id) ON DELETE CASCADE
// and be listed in ProfileScopedTables. Guards future migrations.
func TestProfileScopedTablesCascade(t *testing.T) {
	db := openTest(t).DB()
	var tables []string
	require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables).Error)

	type fk struct {
		Table    string `gorm:"column:table"`
		From     string `gorm:"column:from"`
		OnDelete string `gorm:"column:on_delete"`
	}
	for _, table := range tables {
		if !slices.Contains(columns(t, db, table), "profile_id") {
			continue
		}
		assert.Contains(t, ProfileScopedTables, table, "table %s has profile_id but is not in ProfileScopedTables", table)
		var fks []fk
		require.NoError(t, db.Raw(fmt.Sprintf("PRAGMA foreign_key_list(%q)", table)).Scan(&fks).Error)
		found := false
		for _, f := range fks {
			if f.From == "profile_id" && f.Table == "profiles" && f.OnDelete == "CASCADE" {
				found = true
			}
		}
		assert.True(t, found, "table %s: profile_id must reference profiles(id) ON DELETE CASCADE", table)
	}
}

func TestForeignKeysRejectOrphans(t *testing.T) {
	db := openTest(t).DB()
	err := db.Exec("INSERT INTO list_entries (profile_id, media_type, media_id, status, created_at, updated_at) VALUES (999, 'ANIME', 1, 'CURRENT', 0, 0)").Error
	assert.Error(t, err)
}

func TestChecks(t *testing.T) {
	db := openTest(t).DB()
	a := insertProfile(t, db, "A")
	assert.Error(t, db.Exec("INSERT INTO list_entries (profile_id, media_type, media_id, status, created_at, updated_at) VALUES (?, 'ANIME', 1, 'WATCHING', 0, 0)", a).Error)
	assert.Error(t, db.Exec("INSERT INTO list_entries (profile_id, media_type, media_id, status, progress, created_at, updated_at) VALUES (?, 'ANIME', 1, 'CURRENT', -1, 0, 0)", a).Error)
	assert.Error(t, db.Exec("UPDATE profiles SET score_format = 'STARS' WHERE id = ?", a).Error)
	// Unique, case-insensitive names.
	assert.Error(t, db.Exec("INSERT INTO profiles (name, avatar_id, created_at, updated_at) VALUES ('a', 'x', 0, 0)").Error)
}

// Data written for profile A is never visible through profile B, and deleting A
// removes all of A's rows (and only A's).
func TestIsolationAndCascade(t *testing.T) {
	db := openTest(t).DB()
	a := insertProfile(t, db, "Ana")
	b := insertProfile(t, db, "Bruno")
	seedProfileData(t, db, a, "client-a")
	seedProfileData(t, db, b, "client-b")

	for _, table := range ProfileScopedTables {
		assert.Equal(t, 1, countFor(t, db, table, a), table)
		assert.Equal(t, 1, countFor(t, db, table, b), table)
	}

	// A's rows are never returned when scoping by B.
	var notes []string
	require.NoError(t, db.Raw("SELECT notes FROM list_entries WHERE profile_id = ?", b).Scan(&notes).Error)
	assert.Equal(t, []string{fmt.Sprintf("notes of %d", b)}, notes)
	var items int
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM custom_list_items i JOIN custom_lists l ON l.id = i.list_id WHERE l.profile_id = ?`, b).Scan(&items).Error)
	assert.Equal(t, 1, items)

	// Hard delete of A cascades.
	require.NoError(t, db.Exec("DELETE FROM profiles WHERE id = ?", a).Error)
	for _, table := range ProfileScopedTables {
		assert.Equal(t, 0, countFor(t, db, table, a), table)
		assert.Equal(t, 1, countFor(t, db, table, b), table)
	}
	var totalItems int
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM custom_list_items").Scan(&totalItems).Error)
	assert.Equal(t, 1, totalItems)
	// Global metadata is untouched.
	var snaps int
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM media_snapshots").Scan(&snaps).Error)
	assert.Equal(t, 1, snaps)
}

func insertProfile(t *testing.T, db *gorm.DB, name string) int64 {
	t.Helper()
	now := time.Now().Unix()
	require.NoError(t, db.Exec("INSERT INTO profiles (name, avatar_id, created_at, updated_at) VALUES (?, 'gl-lorelei-01', ?, ?)", name, now, now).Error)
	var id int64
	require.NoError(t, db.Raw("SELECT id FROM profiles WHERE name = ?", name).Scan(&id).Error)
	return id
}

func seedProfileData(t *testing.T, db *gorm.DB, pid int64, clientID string) {
	t.Helper()
	now := time.Now().Unix()
	stmts := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO profile_devices (client_id, profile_id, platform, last_seen_at) VALUES (?, ?, 'web', ?)", []any{clientID, pid, now}},
		{"INSERT INTO list_entries (profile_id, media_type, media_id, status, progress, notes, created_at, updated_at) VALUES (?, 'ANIME', 21, 'CURRENT', 3, ?, ?, ?)", []any{pid, fmt.Sprintf("notes of %d", pid), now, now}},
		{"INSERT INTO favourites (profile_id, kind, ref_id, created_at) VALUES (?, 'ANIME', 21, ?)", []any{pid, now}},
		{"INSERT INTO custom_lists (profile_id, media_type, name, created_at) VALUES (?, 'ANIME', 'Top', ?)", []any{pid, now}},
		{"INSERT INTO custom_list_items (list_id, entry_id, added_at) SELECT l.id, e.id, ? FROM custom_lists l, list_entries e WHERE l.profile_id = ? AND e.profile_id = ?", []any{now, pid, pid}},
		{"INSERT INTO watch_history (profile_id, media_id, episode_number, kind, position_sec, duration, time_added, time_updated) VALUES (?, 21, 3, 'onlinestream', 120, 1440, ?, ?)", []any{pid, now, now}},
		{"INSERT INTO episode_progress (profile_id, media_id, episode_number, position_sec, duration, updated_at) VALUES (?, 21, 3, 120, 1440, ?)", []any{pid, now}},
		{"INSERT INTO activity_log (profile_id, media_type, media_id, kind, created_at) VALUES (?, 'ANIME', 21, 'progress', ?)", []any{pid, now}},
		{"INSERT INTO profile_settings (profile_id, value, updated_at) VALUES (?, '{}', ?)", []any{pid, now}},
		{"INSERT INTO profile_theme (profile_id, value, updated_at) VALUES (?, '{}', ?)", []any{pid, now}},
		{"INSERT INTO profile_autoselect (profile_id, value, updated_at) VALUES (?, '{}', ?)", []any{pid, now}},
		{"INSERT OR IGNORE INTO media_snapshots (media_id, media_type, payload, fetched_at) VALUES (21, 'ANIME', '{}', ?)", []any{now}},
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s.sql, s.args...).Error, s.sql)
	}
}

func countFor(t *testing.T, db *gorm.DB, table string, pid int64) int {
	t.Helper()
	var n int
	require.NoError(t, db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %q WHERE profile_id = ?", table), pid).Scan(&n).Error)
	return n
}

func columns(t *testing.T, db *gorm.DB, table string) []string {
	t.Helper()
	var cols []string
	require.NoError(t, db.Raw(fmt.Sprintf("SELECT name FROM pragma_table_info(%q)", table)).Scan(&cols).Error)
	return cols
}
