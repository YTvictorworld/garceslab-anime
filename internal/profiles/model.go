// Package profiles implements Garceslab Anime's local profiles: the profile
// manager, PINs, session tokens, request resolution and the per-profile data
// store (profiles.db, see store/).
package profiles

import (
	"errors"
	"seanime/internal/profiles/avatars"
)

// Profile is a row of the profiles table. The PIN hash never leaves the server.
type Profile struct {
	ID             int64   `gorm:"column:id;primaryKey" json:"id"`
	Name           string  `gorm:"column:name" json:"name"`
	AvatarID       string  `gorm:"column:avatar_id" json:"avatarId"`
	Color          *string `gorm:"column:color" json:"color,omitempty"`
	PinHash        *string `gorm:"column:pin_hash" json:"-"`
	PinFailed      int     `gorm:"column:pin_failed" json:"-"`
	PinLockedUntil *int64  `gorm:"column:pin_locked_until" json:"-"`
	TokenVersion   int64   `gorm:"column:token_version" json:"-"`
	SortOrder      int     `gorm:"column:sort_order" json:"sortOrder"`
	ScoreFormat    string  `gorm:"column:score_format" json:"scoreFormat"`
	TitleLanguage  string  `gorm:"column:title_language" json:"titleLanguage"`
	DeletedAt      *int64  `gorm:"column:deleted_at" json:"deletedAt,omitempty"`
	CreatedAt      int64   `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt      int64   `gorm:"column:updated_at" json:"updatedAt"`
	LastUsedAt     *int64  `gorm:"column:last_used_at" json:"lastUsedAt,omitempty"`

	// Computed, not stored.
	HasPin    bool   `gorm:"-" json:"hasPin"`
	AvatarURL string `gorm:"-" json:"avatarUrl"`
}

func (Profile) TableName() string { return "profiles" }

// fill sets the computed fields.
func (p *Profile) fill() *Profile {
	if p == nil {
		return nil
	}
	p.HasPin = p.PinHash != nil && *p.PinHash != ""
	if a := avatars.Resolve(p.AvatarID); a != nil {
		p.AvatarURL = a.URL
	}
	return p
}

// IsDeleted reports whether the profile is in the trash.
func (p *Profile) IsDeleted() bool { return p != nil && p.DeletedAt != nil }

// AuditEntry is a row of profile_audit.
type AuditEntry struct {
	ID              int64  `gorm:"column:id;primaryKey" json:"id"`
	ActorProfileID  *int64 `gorm:"column:actor_profile_id" json:"actorProfileId,omitempty"`
	TargetProfileID int64  `gorm:"column:target_profile_id" json:"targetProfileId"`
	TargetName      string `gorm:"column:target_name" json:"targetName"`
	Action          string `gorm:"column:action" json:"action"`
	CreatedAt       int64  `gorm:"column:created_at" json:"createdAt"`
}

func (AuditEntry) TableName() string { return "profile_audit" }

const (
	AuditCreate    = "create"
	AuditUpdate    = "update"
	AuditDelete    = "delete"
	AuditRestore   = "restore"
	AuditPurge     = "purge"
	AuditPinSet    = "pin_set"
	AuditPinChange = "pin_change"
	AuditPinRemove = "pin_remove"
)

// Status is what /api/v1/status exposes about profiles.
type Status struct {
	Enabled    bool `json:"enabled"`
	NeedsSetup bool `json:"needsSetup"`
	MaxLanes   int  `json:"maxLanes"`
	Enforced   bool `json:"enforced"`
}

// MaxLanes is the number of simultaneous playbacks the server will support (phase 2).
const MaxLanes = 3

var validScoreFormats = map[string]bool{
	"POINT_100": true, "POINT_10_DECIMAL": true, "POINT_10": true, "POINT_5": true, "POINT_3": true,
}

var validTitleLanguages = map[string]bool{
	"ROMAJI": true, "ENGLISH": true, "NATIVE": true,
}

// Error codes returned to clients. They are stable strings the clients match on.
var (
	ErrProfileRequired = errors.New("PROFILE_REQUIRED")
	ErrNotFound        = errors.New("PROFILE_NOT_FOUND")
	ErrNameTaken       = errors.New("PROFILE_NAME_TAKEN")
	ErrInvalidName     = errors.New("PROFILE_INVALID_NAME")
	ErrInvalidAvatar   = errors.New("PROFILE_INVALID_AVATAR")
	ErrInvalidColor    = errors.New("PROFILE_INVALID_COLOR")
	ErrInvalidField    = errors.New("PROFILE_INVALID_FIELD")
	ErrInvalidPin      = errors.New("PROFILE_INVALID_PIN") // malformed PIN (not 4-6 digits)
	ErrPinRequired     = errors.New("PROFILE_PIN_REQUIRED")
	ErrWrongPin        = errors.New("PROFILE_WRONG_PIN")
	ErrPinLocked       = errors.New("PROFILE_PIN_LOCKED")
	ErrDeleted         = errors.New("PROFILE_DELETED")
	ErrNotDeleted      = errors.New("PROFILE_NOT_DELETED")
	ErrInvalidToken    = errors.New("PROFILE_INVALID_TOKEN")
	ErrNoClientID      = errors.New("PROFILE_NO_CLIENT_ID")
)
