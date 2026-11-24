package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A phase 1 database (schema v1) with audit rows upgrades to the phase 4 audit
// table: rows and ids are kept, `detail` exists, new actions are accepted and the
// actor still becomes NULL when its profile is purged.
func TestPhase4AuditMigrationUpgradesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := gorm.Open(sqlite.Open(path+"?"+dsnPragmas), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	ctx := context.Background()

	var v1 []migration
	for _, m := range migrations {
		if m.version == 1 {
			v1 = append(v1, m)
		}
	}
	require.NoError(t, applyMigrations(ctx, db, v1))
	a := insertProfile(t, db, "Ana")
	b := insertProfile(t, db, "Bruno")
	require.NoError(t, db.Exec(`INSERT INTO profile_audit (id, actor_profile_id, target_profile_id, target_name, action, created_at)
VALUES (7, ?, ?, 'Bruno', 'pin_set', 100), (8, ?, ?, 'Ana', 'create', 200)`, a, b, a, a).Error)
	// v1 refuses the new actions.
	assert.Error(t, db.Exec("INSERT INTO profile_audit (target_profile_id, target_name, action, created_at) VALUES (1, 'x', 'import', 0)").Error)

	require.NoError(t, applyMigrations(ctx, db, migrations))
	assert.Contains(t, columns(t, db, "profile_audit"), "detail")

	type row struct {
		ID     int64
		Actor  *int64 `gorm:"column:actor_profile_id"`
		Action string
		Detail *string
	}
	var rows []row
	require.NoError(t, db.Raw("SELECT id, actor_profile_id, action, detail FROM profile_audit ORDER BY id").Scan(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, int64(7), rows[0].ID)
	assert.Equal(t, "pin_set", rows[0].Action)
	assert.Nil(t, rows[0].Detail)
	assert.Equal(t, int64(8), rows[1].ID)

	require.NoError(t, db.Exec("INSERT INTO profile_audit (actor_profile_id, target_profile_id, target_name, action, detail, created_at) VALUES (?, ?, 'Bruno', 'import', 'AniList list', 300)", a, b).Error)
	assert.Error(t, db.Exec("INSERT INTO profile_audit (target_profile_id, target_name, action, created_at) VALUES (1, 'x', '', 0)").Error)

	// The actor FK survived the rebuild.
	require.NoError(t, db.Exec("DELETE FROM profiles WHERE id = ?", a).Error)
	var withActor int
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM profile_audit WHERE actor_profile_id IS NOT NULL").Scan(&withActor).Error)
	assert.Equal(t, 0, withActor)
	var total int
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM profile_audit").Scan(&total).Error)
	assert.Equal(t, 3, total)

	var idx int
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('ix_profile_audit_time','ix_profile_audit_target')").Scan(&idx).Error)
	assert.Equal(t, 2, idx)
}
