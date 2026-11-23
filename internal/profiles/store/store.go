// Package store opens profiles.db, the SQLite database that holds local profiles
// and all per-profile user data, and applies its hand-written migrations.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// FileName is the database file name inside the app data directory.
const FileName = "profiles.db"

// dsnPragmas are applied on every new connection by the glebarez/modernc driver.
// Note: the `_foreign_keys=on` syntax used elsewhere in Seanime is mattn-only and is
// ignored by this driver, so the `_pragma=` form is required.
const dsnPragmas = "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)&_pragma=synchronous(NORMAL)"

// ErrForeignKeysDisabled is returned when the connection does not enforce foreign keys.
var ErrForeignKeysDisabled = errors.New("profiles store: PRAGMA foreign_keys is not enabled")

type Store struct {
	db   *gorm.DB
	path string
}

// Open opens (or creates) <dataDir>/profiles.db, checks that foreign keys are
// enforced and runs pending migrations. An empty dataDir opens an in-memory
// database (single connection), which is meant for tests.
func Open(dataDir string) (*Store, error) {
	path := ":memory:"
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, fmt.Errorf("profiles store: create data dir: %w", err)
		}
		path = filepath.Join(dataDir, FileName)
	}
	return OpenPath(path)
}

// OpenPath opens the database at an explicit path (":memory:" allowed).
func OpenPath(path string) (*Store, error) {
	gdb, err := gorm.Open(sqlite.Open(path+"?"+dsnPragmas), &gorm.Config{
		Logger:                 gormlogger.Discard,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("profiles store: open %s: %w", path, err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// Every connection to ":memory:" is a separate database.
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(4)
		sqlDB.SetMaxIdleConns(2)
		sqlDB.SetConnMaxLifetime(time.Hour)
	}

	s := &Store{db: gdb, path: path}

	if err := s.checkForeignKeys(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	if err := migrate(context.Background(), gdb); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	return s, nil
}

func (s *Store) checkForeignKeys() error {
	var fk int
	if err := s.db.Raw("PRAGMA foreign_keys").Scan(&fk).Error; err != nil {
		return fmt.Errorf("profiles store: read foreign_keys pragma: %w", err)
	}
	if fk != 1 {
		return ErrForeignKeysDisabled
	}
	return nil
}

// DB returns the GORM handle. Schema changes must go through migrations.
func (s *Store) DB() *gorm.DB { return s.db }

// Path returns the database path.
func (s *Store) Path() string { return s.path }

// SchemaVersion returns the applied schema version.
func (s *Store) SchemaVersion() (int, error) {
	return currentVersion(s.db)
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// ProfileScopedTables are the tables with a direct `profile_id` foreign key
// (ON DELETE CASCADE). custom_list_items is scoped through custom_lists and
// list_entries. Every query on these tables must filter by profile_id.
var ProfileScopedTables = []string{
	"profile_devices",
	"list_entries",
	"favourites",
	"custom_lists",
	"watch_history",
	"episode_progress",
	"activity_log",
	"profile_settings",
	"profile_theme",
	"profile_autoselect",
}
