package db_bridge

import (
	"context"
	"errors"
	"seanime/internal/database/db"
	"seanime/internal/database/models"
	"seanime/internal/library/anime"

	"github.com/goccy/go-json"
	"gorm.io/gorm"
)

func FindAutoSelectProfile(db *db.Database) (*anime.AutoSelectProfile, bool) {
	profile, err := GetAutoSelectProfile(db)
	return profile, err == nil && profile.DbID != 0
}

// ProfileAutoSelectHook (profiles-hook) returns the auto-select profile of the
// local profile that owns the playback, if it has one.
var ProfileAutoSelectHook func() (*anime.AutoSelectProfile, bool)

// ProfileAutoSelectCtxHook (profiles-hook, phase 2) returns the auto-select
// profile of the local profile in ctx (the request's profile; nil if it has
// none) and whether ctx carries a profile at all.
var ProfileAutoSelectCtxHook func(ctx context.Context) (p *anime.AutoSelectProfile, hasProfile bool)

// FindAutoSelectProfileCtx is FindAutoSelectProfile for the profile in ctx: with
// several playbacks at once, a stream start uses the profile that asked for it
// instead of the playback lane owner's (profiles-hook, phase 2).
func FindAutoSelectProfileCtx(ctx context.Context, db *db.Database) (*anime.AutoSelectProfile, bool) {
	if ProfileAutoSelectCtxHook != nil && ctx != nil {
		if p, hasProfile := ProfileAutoSelectCtxHook(ctx); hasProfile {
			if p != nil {
				return p, true
			}
			// The profile has none of its own: the server's, never another profile's.
			g, err := GetGlobalAutoSelectProfile(db)
			return g, err == nil && g.DbID != 0
		}
	}
	return FindAutoSelectProfile(db)
}

// GetAutoSelectProfile returns the single autoselect profile if it exists
func GetAutoSelectProfile(db *db.Database) (*anime.AutoSelectProfile, error) {
	if ProfileAutoSelectHook != nil { // profiles-hook
		if p, ok := ProfileAutoSelectHook(); ok {
			return p, nil
		}
	}
	return GetGlobalAutoSelectProfile(db)
}

// GetGlobalAutoSelectProfile returns the global (server) autoselect profile (profiles-hook).
func GetGlobalAutoSelectProfile(db *db.Database) (*anime.AutoSelectProfile, error) {
	var res models.AutoSelectProfile
	err := db.Gorm().First(&res).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &anime.AutoSelectProfile{}, nil
		}
		return nil, err
	}

	// Unmarshal the data
	var profile anime.AutoSelectProfile
	if err := json.Unmarshal(res.Value, &profile); err != nil {
		return nil, err
	}
	profile.DbID = res.ID

	return &profile, nil
}

// SaveAutoSelectProfile saves or updates the autoselect profile
// Since there's only one profile at all time, this will create or update it
func SaveAutoSelectProfile(db *db.Database, profile *anime.AutoSelectProfile) error {
	// Marshal the data
	bytes, err := json.Marshal(profile)
	if err != nil {
		return err
	}

	// Check if a profile already exists
	var existing models.AutoSelectProfile
	err = db.Gorm().First(&existing).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Create new profile
		return db.Gorm().Create(&models.AutoSelectProfile{
			Value: bytes,
		}).Error
	} else if err != nil {
		return err
	}

	// Update existing profile
	return db.Gorm().Model(&models.AutoSelectProfile{}).
		Where("id = ?", existing.ID).
		Update("value", bytes).Error
}

// DeleteAutoSelectProfile deletes the autoselect profile
func DeleteAutoSelectProfile(db *db.Database) error {
	return db.Gorm().Where("1 = 1").Delete(&models.AutoSelectProfile{}).Error
}
