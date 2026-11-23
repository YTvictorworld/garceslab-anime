package profiles

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestService(t *testing.T) (*Service, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	s, err := Open(Options{DataDir: t.TempDir(), Now: clock.Now})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, clock
}

func strp(s string) *string { return &s }

const avatar = "gl-lorelei-01"

func TestNeedsSetup(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	st := s.Status(ctx)
	assert.True(t, st.Enabled)
	assert.True(t, st.NeedsSetup)
	assert.Equal(t, 3, st.MaxLanes)
	assert.False(t, st.Enforced)

	p, err := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})
	require.NoError(t, err)
	assert.False(t, s.Status(ctx).NeedsSetup)

	// A trash-only server needs setup again.
	require.NoError(t, s.Delete(ctx, p.ID))
	assert.True(t, s.Status(ctx).NeedsSetup)
}

func TestCreateValidation(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()

	p, err := s.Create(ctx, CreateInput{Name: "  Ana  ", AvatarID: avatar, Color: strp("#ff8800")})
	require.NoError(t, err)
	assert.Equal(t, "Ana", p.Name)
	assert.False(t, p.HasPin)
	assert.Equal(t, "/profile-avatars/gl-lorelei-01.svg", p.AvatarURL)
	assert.Equal(t, "POINT_10_DECIMAL", p.ScoreFormat)

	_, err = s.Create(ctx, CreateInput{Name: "ANA", AvatarID: avatar})
	assert.ErrorIs(t, err, ErrNameTaken)
	_, err = s.Create(ctx, CreateInput{Name: "", AvatarID: avatar})
	assert.ErrorIs(t, err, ErrInvalidName)
	_, err = s.Create(ctx, CreateInput{Name: strings.Repeat("x", MaxNameLength+1), AvatarID: avatar})
	assert.ErrorIs(t, err, ErrInvalidName)
	_, err = s.Create(ctx, CreateInput{Name: "Bea", AvatarID: "crunchyroll-01"})
	assert.ErrorIs(t, err, ErrInvalidAvatar)
	_, err = s.Create(ctx, CreateInput{Name: "Bea", AvatarID: avatar, Color: strp("red")})
	assert.ErrorIs(t, err, ErrInvalidColor)
	_, err = s.Create(ctx, CreateInput{Name: "Bea", AvatarID: avatar, Pin: strp("12a4")})
	assert.ErrorIs(t, err, ErrInvalidPin)

	b, err := s.Create(ctx, CreateInput{Name: "Bea", AvatarID: avatar, Pin: strp("1234")})
	require.NoError(t, err)
	assert.True(t, b.HasPin)
	assert.Equal(t, p.SortOrder+1, b.SortOrder)

	// Rename onto an existing name, case-insensitively.
	_, err = s.Update(ctx, b.ID, UpdateInput{Name: strp("ana")})
	assert.ErrorIs(t, err, ErrNameTaken)
	// Renaming to itself with another case is fine.
	b, err = s.Update(ctx, b.ID, UpdateInput{Name: strp("BEA"), AvatarID: strp("gl-thumbs-01"), ScoreFormat: strp("POINT_100")})
	require.NoError(t, err)
	assert.Equal(t, "BEA", b.Name)
	assert.Equal(t, "gl-thumbs-01", b.AvatarID)
	assert.Equal(t, "POINT_100", b.ScoreFormat)
	_, err = s.Update(ctx, b.ID, UpdateInput{ScoreFormat: strp("STARS")})
	assert.ErrorIs(t, err, ErrInvalidField)
	_, err = s.Update(ctx, b.ID, UpdateInput{AvatarID: strp("nope")})
	assert.ErrorIs(t, err, ErrInvalidAvatar)
	_, err = s.Update(ctx, 999, UpdateInput{Name: strp("X")})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestPinHashVerify(t *testing.T) {
	h, err := HashPin("4321")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(h, "$argon2id$v=19$"))
	assert.True(t, VerifyPin("4321", h))
	assert.False(t, VerifyPin("4322", h))
	assert.False(t, VerifyPin("4321", "garbage"))
	h2, _ := HashPin("4321")
	assert.NotEqual(t, h, h2, "salted")

	for _, bad := range []string{"", "123", "1234567", "12 4", "١٢٣٤"} {
		assert.ErrorIs(t, ValidatePinFormat(bad), ErrInvalidPin, bad)
	}
	for _, ok := range []string{"0000", "12345", "999999"} {
		assert.NoError(t, ValidatePinFormat(ok))
	}
}

func TestSelectWithoutPin(t *testing.T) {
	s, clock := newTestService(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})
	require.NoError(t, err)

	sess, err := s.Select(ctx, p.ID, "", "client-1", "web")
	require.NoError(t, err)
	assert.Equal(t, clock.Now().Add(MaxTokenTTL).Unix(), sess.ExpiresAt)
	assert.NotNil(t, sess.Profile.LastUsedAt)
	assert.Equal(t, p.ID, s.DeviceProfile(ctx, "client-1"))

	got, err := s.Resolve(ctx, sess.ProfileToken, "client-1")
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)

	_, err = s.Select(ctx, p.ID, "", "", "web")
	assert.ErrorIs(t, err, ErrNoClientID)
	_, err = s.Select(ctx, 999, "", "client-1", "web")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestTokenBindingAndExpiry(t *testing.T) {
	s, clock := newTestService(t)
	ctx := context.Background()
	a, _ := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})
	b, _ := s.Create(ctx, CreateInput{Name: "Bea", AvatarID: avatar})
	sess, err := s.Select(ctx, a.ID, "", "client-1", "")
	require.NoError(t, err)

	// Bound to the client id.
	_, err = s.Resolve(ctx, sess.ProfileToken, "client-2")
	assert.ErrorIs(t, err, ErrInvalidToken)

	// Can't be re-targeted to another profile.
	forged := strings.Replace(sess.ProfileToken, ".1.", ".2.", 1)
	require.NotEqual(t, sess.ProfileToken, forged)
	_ = b
	_, err = s.Resolve(ctx, forged, "client-1")
	assert.ErrorIs(t, err, ErrInvalidToken)

	// Can't extend the expiry.
	parts := strings.Split(sess.ProfileToken, ".")
	parts[2] = "9999999999"
	_, err = s.Resolve(ctx, strings.Join(parts, "."), "client-1")
	assert.ErrorIs(t, err, ErrInvalidToken)

	// Garbage.
	for _, bad := range []string{"", "x", "gp1.1.2", "gp1.a.b.c", "gp2.1.2.c"} {
		_, err = s.Resolve(ctx, bad, "client-1")
		assert.ErrorIs(t, err, ErrInvalidToken, bad)
	}

	// Valid just before expiry, invalid after.
	clock.Advance(MaxTokenTTL - time.Second)
	_, err = s.Resolve(ctx, sess.ProfileToken, "client-1")
	assert.NoError(t, err)
	clock.Advance(time.Second)
	_, err = s.Resolve(ctx, sess.ProfileToken, "client-1")
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestTokenTTLIsCapped(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	s, err := Open(Options{DataDir: t.TempDir(), Now: clock.Now, TokenTTL: 30 * 24 * time.Hour})
	require.NoError(t, err)
	defer s.Close()
	p, _ := s.Create(context.Background(), CreateInput{Name: "Ana", AvatarID: avatar})
	sess, err := s.Select(context.Background(), p.ID, "", "c", "")
	require.NoError(t, err)
	assert.Equal(t, clock.Now().Add(MaxTokenTTL).Unix(), sess.ExpiresAt)
}

func TestSecretPersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(Options{DataDir: dir})
	require.NoError(t, err)
	p, _ := s1.Create(context.Background(), CreateInput{Name: "Ana", AvatarID: avatar})
	sess, err := s1.Select(context.Background(), p.ID, "", "c", "")
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	s2, err := Open(Options{DataDir: dir})
	require.NoError(t, err)
	defer s2.Close()
	_, err = s2.Resolve(context.Background(), sess.ProfileToken, "c")
	assert.NoError(t, err)
}

func TestPinFlowAndLockout(t *testing.T) {
	s, clock := newTestService(t)
	ctx := context.Background()
	p, _ := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})

	// No PIN: select works, keep the session to check invalidation later.
	old, err := s.Select(ctx, p.ID, "", "c1", "")
	require.NoError(t, err)

	require.NoError(t, s.SetPin(ctx, p.ID, strp("1234")))
	_, err = s.Resolve(ctx, old.ProfileToken, "c1")
	assert.ErrorIs(t, err, ErrInvalidToken, "setting a PIN closes open sessions")

	_, err = s.Select(ctx, p.ID, "", "c1", "")
	assert.ErrorIs(t, err, ErrPinRequired)
	sess, err := s.Select(ctx, p.ID, "1234", "c1", "")
	require.NoError(t, err)

	// 4 failures, then a success resets the counter.
	for i := 0; i < MaxPinFailures-1; i++ {
		_, err = s.Select(ctx, p.ID, "0000", "c1", "")
		assert.ErrorIs(t, err, ErrWrongPin)
	}
	_, err = s.Select(ctx, p.ID, "1234", "c1", "")
	require.NoError(t, err)

	// 5 failures lock the profile for 5 minutes, even with the right PIN.
	for i := 0; i < MaxPinFailures-1; i++ {
		_, err = s.Select(ctx, p.ID, "0000", "c1", "")
		assert.ErrorIs(t, err, ErrWrongPin)
	}
	_, err = s.Select(ctx, p.ID, "0000", "c1", "")
	var locked *PinLockedError
	require.ErrorAs(t, err, &locked)
	assert.ErrorIs(t, err, ErrPinLocked)
	assert.Equal(t, clock.Now().Add(PinLockDuration).Unix(), locked.Until)
	_, err = s.Select(ctx, p.ID, "1234", "c1", "")
	assert.ErrorIs(t, err, ErrPinLocked)

	clock.Advance(PinLockDuration)
	_, err = s.Select(ctx, p.ID, "1234", "c1", "")
	require.NoError(t, err)

	// Changing the PIN invalidates sessions; removing it too.
	require.NoError(t, s.SetPin(ctx, p.ID, strp("5678")))
	_, err = s.Resolve(ctx, sess.ProfileToken, "c1")
	assert.ErrorIs(t, err, ErrInvalidToken)
	sess, err = s.Select(ctx, p.ID, "5678", "c1", "")
	require.NoError(t, err)
	require.NoError(t, s.SetPin(ctx, p.ID, nil))
	_, err = s.Resolve(ctx, sess.ProfileToken, "c1")
	assert.ErrorIs(t, err, ErrInvalidToken)
	_, err = s.Select(ctx, p.ID, "", "c1", "")
	require.NoError(t, err)

	assert.ErrorIs(t, s.SetPin(ctx, p.ID, strp("12")), ErrInvalidPin)

	// Audit trail: set, change, remove.
	log, err := s.AuditLog(ctx, 0)
	require.NoError(t, err)
	var actions []string
	for _, e := range log {
		actions = append(actions, e.Action)
	}
	assert.Equal(t, []string{AuditPinRemove, AuditPinChange, AuditPinSet, AuditCreate}, actions)
}

func TestSoftDeleteRestorePurge(t *testing.T) {
	s, clock := newTestService(t)
	ctx := context.Background()
	a, _ := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})
	b, _ := s.Create(ctx, CreateInput{Name: "Bea", AvatarID: avatar})
	sess, err := s.Select(ctx, a.ID, "", "c1", "")
	require.NoError(t, err)

	// Bea deletes Ana (no roles): audited with the actor.
	actorCtx := WithProfile(ctx, b)
	require.NoError(t, s.Delete(actorCtx, a.ID))
	_, err = s.Resolve(ctx, sess.ProfileToken, "c1")
	assert.ErrorIs(t, err, ErrInvalidToken, "deleted profile sessions end")
	_, err = s.Select(ctx, a.ID, "", "c1", "")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Zero(t, s.DeviceProfile(ctx, "c1"))

	list, _ := s.List(ctx, false)
	require.Len(t, list, 1)
	all, _ := s.List(ctx, true)
	require.Len(t, all, 2)

	// The name stays reserved while in the trash.
	_, err = s.Create(ctx, CreateInput{Name: "ana", AvatarID: avatar})
	assert.ErrorIs(t, err, ErrNameTaken)
	_, err = s.Update(ctx, a.ID, UpdateInput{Name: strp("X")})
	assert.ErrorIs(t, err, ErrDeleted)

	restored, err := s.Restore(actorCtx, a.ID)
	require.NoError(t, err)
	assert.Nil(t, restored.DeletedAt)
	_, err = s.Restore(ctx, a.ID)
	assert.ErrorIs(t, err, ErrNotDeleted)

	// Purge only after the retention period; the cascade removes the data.
	require.NoError(t, s.Delete(actorCtx, a.ID))
	require.NoError(t, s.DB().Exec("INSERT INTO list_entries (profile_id, media_type, media_id, status, created_at, updated_at) VALUES (?, 'ANIME', 1, 'CURRENT', 0, 0)", a.ID).Error)
	n, err := s.PurgeDeleted(ctx, TrashRetention)
	require.NoError(t, err)
	assert.Zero(t, n)
	clock.Advance(TrashRetention)
	n, err = s.PurgeDeleted(ctx, TrashRetention)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = s.Get(ctx, a.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	var entries int
	require.NoError(t, s.DB().Raw("SELECT COUNT(*) FROM list_entries").Scan(&entries).Error)
	assert.Zero(t, entries)

	log, _ := s.AuditLog(ctx, 0)
	require.NotEmpty(t, log)
	assert.Equal(t, AuditPurge, log[0].Action)
	assert.Equal(t, a.ID, log[0].TargetProfileID)
	assert.Equal(t, "Ana", log[0].TargetName)
	var deleteEntry *ProfileAuditEntry
	for _, e := range log {
		if e.Action == AuditDelete {
			deleteEntry = e
			break
		}
	}
	require.NotNil(t, deleteEntry)
	require.NotNil(t, deleteEntry.ActorProfileID)
	assert.Equal(t, b.ID, *deleteEntry.ActorProfileID)
}
