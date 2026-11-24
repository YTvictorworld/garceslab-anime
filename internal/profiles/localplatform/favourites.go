package localplatform

// Phase 3: favourites, like AniList's (anime, manga, characters, staff, studios).
// Only anime favourites are shown by the clients for now; the other kinds are kept
// so an AniList import or a JSON export doesn't lose them.

import (
	"context"
	"errors"
	"seanime/internal/api/anilist"
	"seanime/internal/customsource"
	"strings"

	"gorm.io/gorm"
)

const (
	FavouriteAnime     = "ANIME"
	FavouriteManga     = "MANGA"
	FavouriteCharacter = "CHARACTER"
	FavouriteStaff     = "STAFF"
	FavouriteStudio    = "STUDIO"
	// MaxFavourites caps the favourites of one kind (AniList has no hard limit, but
	// an import must not grow without bounds).
	MaxFavourites = 5000
)

var (
	ErrInvalidFavouriteKind = errors.New("invalid favourite kind")
	ErrTooManyFavourites    = errors.New("too many favourites")
)

// ProfileFavourite is one of the profile's favourites. Media is set for anime
// favourites when the metadata is available.
type ProfileFavourite struct {
	Kind      string             `gorm:"column:kind" json:"kind"`
	RefID     int                `gorm:"column:ref_id" json:"refId"`
	Position  int                `gorm:"column:position" json:"position"`
	CreatedAt int64              `gorm:"column:created_at" json:"createdAt"`
	Media     *anilist.BaseAnime `gorm:"-" json:"media,omitempty"`
}

type favouriteRow struct {
	ProfileID int64  `gorm:"column:profile_id"`
	Kind      string `gorm:"column:kind"`
	RefID     int    `gorm:"column:ref_id"`
	Position  int    `gorm:"column:position"`
	CreatedAt int64  `gorm:"column:created_at"`
}

func (favouriteRow) TableName() string { return "favourites" }

// NormalizeFavouriteKind accepts the kind in any case ("anime", "ANIME").
func NormalizeFavouriteKind(kind string) (string, error) {
	switch k := strings.ToUpper(strings.TrimSpace(kind)); k {
	case FavouriteAnime, FavouriteManga, FavouriteCharacter, FavouriteStaff, FavouriteStudio:
		return k, nil
	}
	return "", ErrInvalidFavouriteKind
}

// Favourites returns the profile's favourites of a kind ("" = every kind), in
// their position order. Anime favourites carry their media.
func (p *Platform) Favourites(ctx context.Context, kind string) ([]*ProfileFavourite, error) {
	q := p.db.WithContext(ctx).Model(&favouriteRow{}).Select("kind, ref_id, position, created_at").Where("profile_id = ?", p.profileID)
	if kind != "" {
		k, err := NormalizeFavouriteKind(kind)
		if err != nil {
			return nil, err
		}
		q = q.Where("kind = ?", k)
	}
	ret := make([]*ProfileFavourite, 0)
	if err := q.Order("kind, position, created_at").Scan(&ret).Error; err != nil {
		return nil, err
	}
	ids := make([]int, 0)
	for _, f := range ret {
		if f.Kind == FavouriteAnime {
			ids = append(ids, f.RefID)
		}
	}
	if len(ids) > 0 {
		media, err := p.meta.GetBaseAnimeMany(ctx, ids)
		if err != nil {
			p.logger.Debug().Err(err).Msg("profiles: Could not load the favourites metadata")
		}
		for _, f := range ret {
			if f.Kind == FavouriteAnime {
				f.Media = media[f.RefID]
			}
		}
	}
	return ret, nil
}

// IsFavourite reports whether a media/character/... is one of the profile's favourites.
func (p *Platform) IsFavourite(ctx context.Context, kind string, refID int) (bool, error) {
	k, err := NormalizeFavouriteKind(kind)
	if err != nil {
		return false, err
	}
	var n int64
	err = p.db.WithContext(ctx).Model(&favouriteRow{}).Where("profile_id = ? AND kind = ? AND ref_id = ?", p.profileID, k, refID).Count(&n).Error
	return n > 0, err
}

// AddFavourite adds a favourite at the end (no-op if it already is one). Anime must
// exist on AniList (its snapshot is stored, like a list entry's).
func (p *Platform) AddFavourite(ctx context.Context, kind string, refID int) error {
	k, err := NormalizeFavouriteKind(kind)
	if err != nil {
		return err
	}
	if refID <= 0 || customsource.IsExtensionId(refID) {
		return ErrMediaNotFound
	}
	if k == FavouriteAnime {
		if _, err := p.meta.EnsureAnime(ctx, refID); err != nil {
			return errors.Join(ErrMediaNotFound, err)
		}
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := p.addFavouriteTx(tx, k, refID, p.now().Unix())
		return err
	})
}

func (p *Platform) addFavouriteTx(tx *gorm.DB, kind string, refID int, createdAt int64) (bool, error) {
	var count int64
	var pos int
	if err := tx.Raw("SELECT COUNT(*), COALESCE(MAX(position), -1) + 1 FROM favourites WHERE profile_id = ? AND kind = ?", p.profileID, kind).
		Row().Scan(&count, &pos); err != nil {
		return false, err
	}
	if count >= MaxFavourites {
		return false, ErrTooManyFavourites
	}
	res := tx.Exec("INSERT OR IGNORE INTO favourites (profile_id, kind, ref_id, position, created_at) VALUES (?, ?, ?, ?, ?)",
		p.profileID, kind, refID, pos, createdAt)
	return res.RowsAffected > 0, res.Error
}

// RemoveFavourite removes a favourite (no-op if it isn't one).
func (p *Platform) RemoveFavourite(ctx context.Context, kind string, refID int) error {
	k, err := NormalizeFavouriteKind(kind)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.db.WithContext(ctx).Where("profile_id = ? AND kind = ? AND ref_id = ?", p.profileID, k, refID).Delete(&favouriteRow{}).Error
}

// ImportFavourites adds favourites (AniList import, JSON import). Replace first
// removes the profile's favourites of the kinds being imported. Invalid rows are
// skipped. Returns how many were added.
func (p *Platform) ImportFavourites(ctx context.Context, favs []ProfileFavourite, mode ImportMode) (int, error) {
	if mode != ImportReplace && mode != ImportMerge {
		return 0, errors.New("invalid import mode")
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	added := 0
	now := p.now().Unix()
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if mode == ImportReplace {
			kinds := map[string]bool{}
			for _, f := range favs {
				if k, err := NormalizeFavouriteKind(f.Kind); err == nil {
					kinds[k] = true
				}
			}
			for k := range kinds {
				if err := tx.Where("profile_id = ? AND kind = ?", p.profileID, k).Delete(&favouriteRow{}).Error; err != nil {
					return err
				}
			}
		}
		for _, f := range favs {
			k, err := NormalizeFavouriteKind(f.Kind)
			if err != nil || f.RefID <= 0 || customsource.IsExtensionId(f.RefID) {
				continue
			}
			createdAt := f.CreatedAt
			if createdAt <= 0 {
				createdAt = now
			}
			ok, err := p.addFavouriteTx(tx, k, f.RefID, createdAt)
			if errors.Is(err, ErrTooManyFavourites) {
				continue
			}
			if err != nil {
				return err
			}
			if ok {
				added++
			}
		}
		return nil
	})
	return added, err
}
