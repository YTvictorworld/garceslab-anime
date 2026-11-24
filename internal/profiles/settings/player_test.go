package settings

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"seanime/internal/profiles/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPlayerTestStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := store.Open("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	ids := make([]int64, 0, 2)
	for _, name := range []string{"Ana", "Bruno"} {
		require.NoError(t, db.Exec("INSERT INTO profiles (name, avatar_id, created_at, updated_at) VALUES (?, 'gl-lorelei-01', 0, 0)", name).Error)
		var id int64
		require.NoError(t, db.Raw("SELECT id FROM profiles WHERE name = ?", name).Scan(&id).Error)
		ids = append(ids, id)
	}
	s := NewStore(db)
	s.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	return s, ids[0], ids[1]
}

func patchOf(t *testing.T, js string) PlayerPrefsPatch {
	t.Helper()
	var p PlayerPrefsPatch
	require.NoError(t, json.Unmarshal([]byte(js), &p))
	return p
}

// Each profile has its own player preferences; one profile's changes never
// show up in another's.
func TestPlayerPrefsArePerProfile(t *testing.T) {
	s, ana, bruno := newPlayerTestStore(t)
	ctx := context.Background()

	got, err := s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"tv": {"preferredAudioLanguages": "spa, es", "subtitleFontSize": 60, "autoSkipOpEd": true}}`))
	require.NoError(t, err)
	require.NotNil(t, got.TV)
	assert.Equal(t, "spa, es", *got.TV.PreferredAudioLanguages)
	assert.Equal(t, int64(1_800_000_000), got.UpdatedAt)

	_, err = s.PatchPlayerPrefs(ctx, bruno, patchOf(t, `{"tv": {"preferredAudioLanguages": "jpn"}, "web": {"mediaCore": {"volume": 0.4}}}`))
	require.NoError(t, err)

	a, err := s.PlayerPrefs(ctx, ana)
	require.NoError(t, err)
	b, err := s.PlayerPrefs(ctx, bruno)
	require.NoError(t, err)
	assert.Equal(t, "spa, es", *a.TV.PreferredAudioLanguages)
	assert.Equal(t, 60, *a.TV.SubtitleFontSize)
	assert.Nil(t, a.Web)
	assert.Equal(t, "jpn", *b.TV.PreferredAudioLanguages)
	assert.Nil(t, b.TV.SubtitleFontSize)
	assert.JSONEq(t, `{"volume": 0.4}`, string(b.Web.MediaCore))

	// A profile without preferences reads as empty.
	require.NoError(t, s.db.Exec("INSERT INTO profiles (name, avatar_id, created_at, updated_at) VALUES ('Carla', 'gl-lorelei-01', 0, 0)").Error)
	var carla int64
	require.NoError(t, s.db.Raw("SELECT id FROM profiles WHERE name = 'Carla'").Scan(&carla).Error)
	c, err := s.PlayerPrefs(ctx, carla)
	require.NoError(t, err)
	assert.Nil(t, c.TV)
	assert.Nil(t, c.Web)
}

// A patch changes only the fields it sends, and keeps the other settings.
func TestPlayerPrefsPatchMerges(t *testing.T) {
	s, ana, _ := newPlayerTestStore(t)
	ctx := context.Background()
	off := false
	require.NoError(t, s.Save(ctx, ana, &ProfileSettings{MarkWatchingOnStart: &off}))

	_, err := s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"tv": {"speed": 1.5, "showSubtitles": false}, "web": {"videoCore": {"preferredAudioLanguage": "jpn"}}}`))
	require.NoError(t, err)
	got, err := s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"tv": {"skipDurationSec": 90}}`))
	require.NoError(t, err)
	assert.Equal(t, 1.5, *got.TV.Speed)
	assert.False(t, *got.TV.ShowSubtitles)
	assert.Equal(t, 90, *got.TV.SkipDurationSec)
	assert.NotNil(t, got.Web)

	st, err := s.Get(ctx, ana)
	require.NoError(t, err)
	require.NotNil(t, st.MarkWatchingOnStart)
	assert.False(t, *st.MarkWatchingOnStart, "other settings are kept")

	got, err = s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"web": null}`))
	require.NoError(t, err)
	assert.Nil(t, got.Web)
	assert.NotNil(t, got.TV)
}

func TestPlayerPrefsValidation(t *testing.T) {
	s, ana, _ := newPlayerTestStore(t)
	ctx := context.Background()
	for _, bad := range []string{
		`{"tv": {"subtitleFontSize": 5000}}`,
		`{"tv": {"speed": 0}}`,
		`{"tv": {"skipDurationSec": -1}}`,
		`{"tv": {"preferredAudioLanguages": 3}}`,
		`{"tv": {"wyzieApiKey": "secret"}}`,            // device-only, not accepted
		`{"tv": {"externalPlayerTemplate": "vlc://"}}`, // device-only
		`{"web": {"videoCore": [1, 2]}}`,
		`{"web": {"keybindings": {}}}`,
		`{"phone": {}}`,
	} {
		_, err := s.PatchPlayerPrefs(ctx, ana, patchOf(t, bad))
		assert.ErrorIs(t, err, ErrInvalidSetting, bad)
	}
	got, err := s.PlayerPrefs(ctx, ana)
	require.NoError(t, err)
	assert.Nil(t, got.TV, "nothing was saved")

	// The generic settings PATCH path validates the same way.
	assert.ErrorIs(t, (&ProfileSettings{Player: json.RawMessage(`{"tv": {"speed": 99}}`)}).Validate(), ErrInvalidSetting)
	assert.NoError(t, (&ProfileSettings{Player: json.RawMessage(`{"tv": {"speed": 2}}`)}).Validate())
}

// Purging a profile removes its preferences with the rest of its settings.
func TestPlayerPrefsCascadeOnPurge(t *testing.T) {
	s, ana, bruno := newPlayerTestStore(t)
	ctx := context.Background()
	_, err := s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"tv": {"autoPlay": false}}`))
	require.NoError(t, err)
	_, err = s.PatchPlayerPrefs(ctx, bruno, patchOf(t, `{"tv": {"autoPlay": true}}`))
	require.NoError(t, err)

	require.NoError(t, s.db.Exec("DELETE FROM profiles WHERE id = ?", ana).Error)
	s.Forget(ana)
	var n int
	require.NoError(t, s.db.Raw("SELECT COUNT(*) FROM profile_settings").Scan(&n).Error)
	assert.Equal(t, 1, n)
	b, err := s.PlayerPrefs(ctx, bruno)
	require.NoError(t, err)
	assert.True(t, *b.TV.AutoPlay)
}

// "Skip filler and recaps" follows the profile like the other TV preferences
// and only accepts a boolean.
func TestPlayerPrefsSkipFillerEpisodes(t *testing.T) {
	s, ana, bruno := newPlayerTestStore(t)
	ctx := context.Background()

	got, err := s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"tv": {"skipFillerEpisodes": true}}`))
	require.NoError(t, err)
	require.NotNil(t, got.TV.SkipFillerEpisodes)
	assert.True(t, *got.TV.SkipFillerEpisodes)

	// Another field later keeps it.
	got, err = s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"tv": {"autoNextEpisode": false}}`))
	require.NoError(t, err)
	assert.True(t, *got.TV.SkipFillerEpisodes)

	b, err := s.PlayerPrefs(ctx, bruno)
	require.NoError(t, err)
	assert.Nil(t, b.TV)

	_, err = s.PatchPlayerPrefs(ctx, ana, patchOf(t, `{"tv": {"skipFillerEpisodes": "yes"}}`))
	require.ErrorIs(t, err, ErrInvalidSetting)
}
