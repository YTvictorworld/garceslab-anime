package profiles

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 4: profile_audit records who did what, with a short detail.
func TestAuditDetails(t *testing.T) {
	s, clock := newTestService(t)
	ctx := context.Background()

	ana, err := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})
	require.NoError(t, err)
	bruno, err := s.Create(ctx, CreateInput{Name: "Bruno", AvatarID: avatar})
	require.NoError(t, err)
	asAna := WithProfile(ctx, ana)

	clock.Advance(1)
	_, err = s.Update(asAna, bruno.ID, UpdateInput{Name: strp("Bruno G")})
	require.NoError(t, err)
	clock.Advance(1)
	_, err = s.Update(asAna, bruno.ID, UpdateInput{AvatarID: strp("gl-thumbs-01"), Color: strp("#ff0000")})
	require.NoError(t, err)
	// Saving the same values again is not a change.
	clock.Advance(1)
	_, err = s.Update(asAna, bruno.ID, UpdateInput{Name: strp("Bruno G"), AvatarID: strp("gl-thumbs-01")})
	require.NoError(t, err)

	clock.Advance(1)
	current, err := s.Get(ctx, bruno.ID)
	require.NoError(t, err)
	s.RecordAudit(asAna, current, AuditImport, "AniList list (merge)")
	clock.Advance(1)
	s.RecordAudit(ctx, current, AuditExport, strings.Repeat("x", 400))

	log, err := s.AuditLog(ctx, 0)
	require.NoError(t, err)
	require.Len(t, log, 6)

	byAction := map[string]*ProfileAuditEntry{}
	for _, e := range log {
		byAction[e.Action] = e
	}
	assert.Equal(t, AuditExport, log[0].Action, "newest first")

	rename := byAction[AuditRename]
	require.NotNil(t, rename)
	require.NotNil(t, rename.Detail)
	assert.Equal(t, "Bruno → Bruno G", *rename.Detail)
	assert.Equal(t, "Bruno", rename.TargetName, "the name before the change")
	require.NotNil(t, rename.ActorProfileID)
	assert.Equal(t, ana.ID, *rename.ActorProfileID)

	update := byAction[AuditUpdate]
	require.NotNil(t, update)
	require.NotNil(t, update.Detail)
	assert.Equal(t, "avatar, colour", *update.Detail)

	imp := byAction[AuditImport]
	require.NotNil(t, imp)
	assert.Equal(t, "AniList list (merge)", *imp.Detail)
	assert.Equal(t, "Bruno G", imp.TargetName)
	require.NotNil(t, imp.ActorProfileID)
	assert.Equal(t, ana.ID, *imp.ActorProfileID)

	exp := byAction[AuditExport]
	require.NotNil(t, exp)
	assert.Nil(t, exp.ActorProfileID, "no profile in the context")
	require.NotNil(t, exp.Detail)
	assert.Equal(t, MaxAuditDetail, utf8.RuneCountInString(*exp.Detail))

	assert.Nil(t, byAction[AuditCreate].Detail)
}

func TestRecordAuditIgnoresNilTarget(t *testing.T) {
	s, _ := newTestService(t)
	s.RecordAudit(context.Background(), nil, AuditImport, "x")
	log, err := s.AuditLog(context.Background(), 0)
	require.NoError(t, err)
	assert.Empty(t, log)
}
