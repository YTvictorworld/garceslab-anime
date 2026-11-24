package profiles

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"seanime/internal/profiles/avatars"
	"seanime/internal/profiles/store"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

const (
	// TrashRetention is how long a deleted profile can be restored before it is purged.
	TrashRetention = 30 * 24 * time.Hour
	// PinLockDuration is how long a profile stays locked after MaxPinFailures wrong PINs.
	PinLockDuration = 5 * time.Minute
	// MaxNameLength is the maximum profile name length in characters.
	MaxNameLength = 32

	// EnforceEnvVar turns on `401 PROFILE_REQUIRED` for user-data routes.
	// Off by default until the local platform (M2) routes user data per profile.
	EnforceEnvVar = "SEANIME_PROFILES_ENFORCE"

	metaTokenSecret = "token_secret"
	purgeInterval   = 6 * time.Hour
	deviceTouchGap  = time.Minute
)

type Options struct {
	// DataDir is the app data dir; profiles.db is created there. Empty = in-memory (tests).
	DataDir string
	// Enforce makes the middleware reject user-data routes without a profile.
	Enforce bool
	// TokenTTL is the session length, capped at MaxTokenTTL. Zero = MaxTokenTTL.
	TokenTTL time.Duration
	Logger   *zerolog.Logger
	// Now is the clock (tests).
	Now func() time.Time
}

// Service is the profile manager.
type Service struct {
	store    *store.Store
	db       *gorm.DB
	signer   tokenSigner
	enforce  bool
	tokenTTL time.Duration
	logger   *zerolog.Logger
	now      func() time.Time

	touchMu   sync.Mutex
	lastTouch map[string]time.Time // clientID -> last profile_devices write

	stopPurge chan struct{}
	stopOnce  sync.Once

	lane *PlaybackLane
}

// EnforceFromEnv reads EnforceEnvVar.
func EnforceFromEnv() bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(EnforceEnvVar)))
	return v
}

// Open opens profiles.db and returns the profile manager. It fails if foreign keys
// can't be enabled, if migrations fail or if the avatar catalog is broken.
func Open(opts Options) (*Service, error) {
	if err := avatars.Validate(); err != nil {
		return nil, err
	}
	st, err := store.Open(opts.DataDir)
	if err != nil {
		return nil, err
	}
	s, err := New(st, opts)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return s, nil
}

// New builds the service on an already opened store.
func New(st *store.Store, opts Options) (*Service, error) {
	logger := opts.Logger
	if logger == nil {
		l := zerolog.Nop()
		logger = &l
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	ttl := opts.TokenTTL
	if ttl <= 0 || ttl > MaxTokenTTL {
		ttl = MaxTokenTTL
	}

	s := &Service{
		store:     st,
		db:        st.DB(),
		enforce:   opts.Enforce,
		tokenTTL:  ttl,
		logger:    logger,
		now:       now,
		lastTouch: map[string]time.Time{},
		stopPurge: make(chan struct{}),
		lane:      &PlaybackLane{now: now},
	}

	secret, err := s.loadOrCreateSecret()
	if err != nil {
		return nil, err
	}
	s.signer = tokenSigner{secret: secret}
	return s, nil
}

// loadOrCreateSecret returns the HMAC secret for profile tokens, generating it on first run.
func (s *Service) loadOrCreateSecret() ([]byte, error) {
	var value string
	err := s.db.Raw("SELECT value FROM profiles_meta WHERE key = ?", metaTokenSecret).Scan(&value).Error
	if err != nil {
		return nil, fmt.Errorf("profiles: read token secret: %w", err)
	}
	if value == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		value = hex.EncodeToString(buf)
		// INSERT OR IGNORE + re-read keeps this safe if two processes race on first run.
		if err := s.db.Exec("INSERT OR IGNORE INTO profiles_meta (key, value) VALUES (?, ?)", metaTokenSecret, value).Error; err != nil {
			return nil, fmt.Errorf("profiles: store token secret: %w", err)
		}
		if err := s.db.Raw("SELECT value FROM profiles_meta WHERE key = ?", metaTokenSecret).Scan(&value).Error; err != nil {
			return nil, err
		}
	}
	return hex.DecodeString(value)
}

func (s *Service) Store() *store.Store { return s.store }
func (s *Service) DB() *gorm.DB        { return s.db }
func (s *Service) Enforced() bool      { return s.enforce }

// Lane returns the (phase 1, single) playback lane.
func (s *Service) Lane() *PlaybackLane { return s.lane }

// SetEnforced toggles enforcement at runtime (tests, future settings).
func (s *Service) SetEnforced(v bool) { s.enforce = v }

// Close stops the purge loop and closes the database.
func (s *Service) Close() error {
	s.stopOnce.Do(func() { close(s.stopPurge) })
	return s.store.Close()
}

// Status returns the profiles block of /api/v1/status.
func (s *Service) Status(ctx context.Context) ProfilesStatus {
	needsSetup, err := s.NeedsSetup(ctx)
	if err != nil {
		s.logger.Error().Err(err).Msg("profiles: count profiles")
	}
	return ProfilesStatus{Enabled: true, NeedsSetup: needsSetup, MaxLanes: MaxLanes, Enforced: s.enforce}
}

// NeedsSetup reports whether there is no active (non-deleted) profile.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Profile{}).Where("deleted_at IS NULL").Count(&n).Error
	return n == 0, err
}

//----------------------------------------------------------------------------------------------------------------------
// CRUD
//----------------------------------------------------------------------------------------------------------------------

// List returns the active profiles (and the trash if includeDeleted) for the selector.
func (s *Service) List(ctx context.Context, includeDeleted bool) ([]*Profile, error) {
	var list []*Profile
	q := s.db.WithContext(ctx).Order("sort_order ASC, id ASC")
	if !includeDeleted {
		q = q.Where("deleted_at IS NULL")
	}
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	for _, p := range list {
		p.fill()
	}
	return list, nil
}

// Get returns a profile, including deleted ones.
func (s *Service) Get(ctx context.Context, id int64) (*Profile, error) {
	var p Profile
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p.fill(), nil
}

type CreateInput struct {
	Name     string  `json:"name"`
	AvatarID string  `json:"avatarId"`
	Color    *string `json:"color,omitempty"`
	Pin      *string `json:"pin,omitempty"` // optional PIN set at creation
}

type UpdateInput struct {
	Name          *string `json:"name,omitempty"`
	AvatarID      *string `json:"avatarId,omitempty"`
	Color         *string `json:"color,omitempty"` // "" clears it
	SortOrder     *int    `json:"sortOrder,omitempty"`
	ScoreFormat   *string `json:"scoreFormat,omitempty"`
	TitleLanguage *string `json:"titleLanguage,omitempty"`
}

var colorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n == 0 || n > MaxNameLength || !utf8.ValidString(name) {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidName
		}
	}
	return name, nil
}

func normalizeColor(c *string) (*string, error) {
	if c == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*c)
	if v == "" {
		return nil, nil
	}
	if !colorRe.MatchString(v) {
		return nil, ErrInvalidColor
	}
	return &v, nil
}

// nameTaken checks case-insensitive uniqueness (the column is COLLATE NOCASE; deleted
// profiles still hold their name until purged).
func nameTaken(tx *gorm.DB, name string, exceptID int64) (bool, error) {
	var n int64
	err := tx.Model(&Profile{}).Where("name = ? COLLATE NOCASE AND id <> ?", name, exceptID).Count(&n).Error
	return n > 0, err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// Create adds a profile. Avatars must exist in the bundled catalog.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Profile, error) {
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	if !avatars.Exists(in.AvatarID) {
		return nil, ErrInvalidAvatar
	}
	color, err := normalizeColor(in.Color)
	if err != nil {
		return nil, err
	}
	var pinHash *string
	if in.Pin != nil && *in.Pin != "" {
		if err := ValidatePinFormat(*in.Pin); err != nil {
			return nil, err
		}
		h, err := HashPin(*in.Pin)
		if err != nil {
			return nil, err
		}
		pinHash = &h
	}

	now := s.now().Unix()
	p := &Profile{
		Name:          name,
		AvatarID:      in.AvatarID,
		Color:         color,
		PinHash:       pinHash,
		TokenVersion:  1,
		ScoreFormat:   "POINT_10_DECIMAL",
		TitleLanguage: "ROMAJI",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if taken, err := nameTaken(tx, name, 0); err != nil {
			return err
		} else if taken {
			return ErrNameTaken
		}
		var maxOrder *int
		if err := tx.Model(&Profile{}).Select("MAX(sort_order)").Scan(&maxOrder).Error; err != nil {
			return err
		}
		if maxOrder != nil {
			p.SortOrder = *maxOrder + 1
		}
		if err := tx.Create(p).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return err
		}
		if err := s.audit(ctx, tx, p, AuditCreate); err != nil {
			return err
		}
		if pinHash != nil {
			return s.audit(ctx, tx, p, AuditPinSet)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p.fill(), nil
}

// Update edits the profile fields present in the input.
func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (*Profile, error) {
	updates := map[string]any{}
	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return nil, err
		}
		updates["name"] = name
	}
	if in.AvatarID != nil {
		if !avatars.Exists(*in.AvatarID) {
			return nil, ErrInvalidAvatar
		}
		updates["avatar_id"] = *in.AvatarID
	}
	if in.Color != nil {
		c, err := normalizeColor(in.Color)
		if err != nil {
			return nil, err
		}
		updates["color"] = c
	}
	if in.SortOrder != nil {
		updates["sort_order"] = *in.SortOrder
	}
	if in.ScoreFormat != nil {
		if !validScoreFormats[*in.ScoreFormat] {
			return nil, ErrInvalidField
		}
		updates["score_format"] = *in.ScoreFormat
	}
	if in.TitleLanguage != nil {
		if !validTitleLanguages[*in.TitleLanguage] {
			return nil, ErrInvalidField
		}
		updates["title_language"] = *in.TitleLanguage
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := getTx(tx, id)
		if err != nil {
			return err
		}
		if p.IsDeleted() {
			return ErrDeleted
		}
		if len(updates) == 0 {
			return nil
		}
		if name, ok := updates["name"].(string); ok {
			if taken, err := nameTaken(tx, name, id); err != nil {
				return err
			} else if taken {
				return ErrNameTaken
			}
		}
		renamed, changed := describeUpdate(p, updates)
		updates["updated_at"] = s.now().Unix()
		if err := tx.Model(&Profile{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrNameTaken
			}
			return err
		}
		if renamed != "" {
			if err := s.auditDetail(ctx, tx, p, AuditRename, renamed); err != nil {
				return err
			}
		}
		if changed != "" {
			return s.auditDetail(ctx, tx, p, AuditUpdate, changed)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete moves a profile to the trash. Its sessions are invalidated; its data stays
// until the purge (TrashRetention).
func (s *Service) Delete(ctx context.Context, id int64) error {
	defer s.lane.ForgetProfile(id)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := getTx(tx, id)
		if err != nil {
			return err
		}
		if p.IsDeleted() {
			return nil
		}
		now := s.now().Unix()
		if err := tx.Model(&Profile{}).Where("id = ?", id).Updates(map[string]any{
			"deleted_at":    now,
			"updated_at":    now,
			"token_version": gorm.Expr("token_version + 1"),
		}).Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM profile_devices WHERE profile_id = ?", id).Error; err != nil {
			return err
		}
		return s.audit(ctx, tx, p, AuditDelete)
	})
}

// Restore takes a profile out of the trash.
func (s *Service) Restore(ctx context.Context, id int64) (*Profile, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := getTx(tx, id)
		if err != nil {
			return err
		}
		if !p.IsDeleted() {
			return ErrNotDeleted
		}
		if err := tx.Model(&Profile{}).Where("id = ?", id).Updates(map[string]any{
			"deleted_at": nil,
			"updated_at": s.now().Unix(),
		}).Error; err != nil {
			return err
		}
		return s.audit(ctx, tx, p, AuditRestore)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// PurgeDeleted permanently removes profiles deleted more than `retention` ago. The
// foreign keys cascade the delete to all their data. Returns the purged count.
func (s *Service) PurgeDeleted(ctx context.Context, retention time.Duration) (int, error) {
	cutoff := s.now().Add(-retention).Unix()
	var victims []*Profile
	if err := s.db.WithContext(ctx).Where("deleted_at IS NOT NULL AND deleted_at <= ?", cutoff).Find(&victims).Error; err != nil {
		return 0, err
	}
	purged := 0
	for _, p := range victims {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.audit(ctx, tx, p, AuditPurge); err != nil {
				return err
			}
			return tx.Exec("DELETE FROM profiles WHERE id = ?", p.ID).Error
		})
		if err != nil {
			return purged, err
		}
		purged++
		s.logger.Info().Int64("profileId", p.ID).Str("name", p.Name).Msg("profiles: Purged deleted profile")
	}
	return purged, nil
}

// StartPurgeLoop purges expired trash now and every few hours until Close.
func (s *Service) StartPurgeLoop() {
	go func() {
		run := func() {
			if _, err := s.PurgeDeleted(context.Background(), TrashRetention); err != nil {
				s.logger.Error().Err(err).Msg("profiles: Purge failed")
			}
		}
		run()
		t := time.NewTicker(purgeInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				run()
			case <-s.stopPurge:
				return
			}
		}
	}()
}

// AuditLog returns the latest audit entries, newest first.
func (s *Service) AuditLog(ctx context.Context, limit int) ([]*ProfileAuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var list []*ProfileAuditEntry
	err := s.db.WithContext(ctx).Order("created_at DESC, id DESC").Limit(limit).Find(&list).Error
	return list, err
}

func (s *Service) audit(ctx context.Context, tx *gorm.DB, target *Profile, action string) error {
	return s.auditDetail(ctx, tx, target, action, "")
}

// auditDetail writes one profile_audit row. The actor is the request's profile
// (none for the purge loop or requests without a profile).
func (s *Service) auditDetail(ctx context.Context, tx *gorm.DB, target *Profile, action, detail string) error {
	e := &ProfileAuditEntry{
		TargetProfileID: target.ID,
		TargetName:      target.Name,
		Action:          action,
		CreatedAt:       s.now().Unix(),
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		if utf8.RuneCountInString(detail) > MaxAuditDetail {
			detail = string([]rune(detail)[:MaxAuditDetail-1]) + "…"
		}
		e.Detail = &detail
	}
	if actor := IDFromContext(ctx); actor != 0 {
		e.ActorProfileID = &actor
	}
	return tx.Create(e).Error
}

// RecordAudit adds an entry for something done to a profile outside this
// service (imports, exports). A failure is logged, never returned: the audit
// log must not break the action itself.
func (s *Service) RecordAudit(ctx context.Context, target *Profile, action, detail string) {
	if target == nil {
		return
	}
	if err := s.auditDetail(ctx, s.db.WithContext(ctx), target, action, detail); err != nil {
		s.logger.Warn().Err(err).Str("action", action).Int64("profileId", target.ID).Msg("profiles: Could not write the audit log")
	}
}

// describeUpdate returns the audit details of an update: "Old → New" when the
// name changes and the list of the other fields that really change.
func describeUpdate(p *Profile, updates map[string]any) (renamed, changed string) {
	if name, ok := updates["name"].(string); ok && name != p.Name {
		renamed = fmt.Sprintf("%s → %s", p.Name, name)
	}
	var fields []string
	if v, ok := updates["avatar_id"].(string); ok && v != p.AvatarID {
		fields = append(fields, "avatar")
	}
	if v, ok := updates["color"]; ok {
		var next string
		switch c := v.(type) {
		case *string:
			if c != nil {
				next = *c
			}
		case string:
			next = c
		}
		prev := ""
		if p.Color != nil {
			prev = *p.Color
		}
		if next != prev {
			fields = append(fields, "colour")
		}
	}
	if v, ok := updates["sort_order"].(int); ok && v != p.SortOrder {
		fields = append(fields, "order")
	}
	if v, ok := updates["score_format"].(string); ok && v != p.ScoreFormat {
		fields = append(fields, "score format")
	}
	if v, ok := updates["title_language"].(string); ok && v != p.TitleLanguage {
		fields = append(fields, "title language")
	}
	return renamed, strings.Join(fields, ", ")
}

func getTx(tx *gorm.DB, id int64) (*Profile, error) {
	var p Profile
	err := tx.Where("id = ?", id).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &p, err
}

//----------------------------------------------------------------------------------------------------------------------
// PIN
//----------------------------------------------------------------------------------------------------------------------

// PinLockedError carries when the lock ends. errors.Is(err, ErrPinLocked) is true.
type PinLockedError struct{ Until int64 }

func (e *PinLockedError) Error() string        { return ErrPinLocked.Error() }
func (e *PinLockedError) Is(target error) bool { return target == ErrPinLocked }

// SetPin sets, changes (pin != nil) or removes (pin == nil or "") the PIN. Any change
// increments token_version, which closes the profile's open sessions.
func (s *Service) SetPin(ctx context.Context, id int64, pin *string) error {
	var newHash *string
	if pin != nil && *pin != "" {
		if err := ValidatePinFormat(*pin); err != nil {
			return err
		}
		h, err := HashPin(*pin)
		if err != nil {
			return err
		}
		newHash = &h
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := getTx(tx, id)
		if err != nil {
			return err
		}
		if p.IsDeleted() {
			return ErrDeleted
		}
		hadPin := p.PinHash != nil && *p.PinHash != ""
		if !hadPin && newHash == nil {
			return nil // nothing to remove
		}
		if err := tx.Model(&Profile{}).Where("id = ?", id).Updates(map[string]any{
			"pin_hash":         newHash,
			"pin_failed":       0,
			"pin_locked_until": nil,
			"token_version":    gorm.Expr("token_version + 1"),
			"updated_at":       s.now().Unix(),
		}).Error; err != nil {
			return err
		}
		action := AuditPinChange
		switch {
		case newHash == nil:
			action = AuditPinRemove
		case !hadPin:
			action = AuditPinSet
		}
		return s.audit(ctx, tx, p, action)
	})
}

// checkPin verifies the PIN with lockout. Profiles without PIN always pass.
func (s *Service) checkPin(ctx context.Context, p *Profile, pin string) error {
	if p.PinHash == nil || *p.PinHash == "" {
		return nil
	}
	now := s.now().Unix()
	if p.PinLockedUntil != nil && *p.PinLockedUntil > now {
		return &PinLockedError{Until: *p.PinLockedUntil}
	}
	if pin == "" {
		return ErrPinRequired
	}
	if VerifyPin(pin, *p.PinHash) {
		if p.PinFailed != 0 || p.PinLockedUntil != nil {
			return s.db.WithContext(ctx).Model(&Profile{}).Where("id = ?", p.ID).
				Updates(map[string]any{"pin_failed": 0, "pin_locked_until": nil}).Error
		}
		return nil
	}

	var lockedUntil *int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE profiles SET pin_failed = pin_failed + 1 WHERE id = ?", p.ID).Error; err != nil {
			return err
		}
		var failed int
		if err := tx.Raw("SELECT pin_failed FROM profiles WHERE id = ?", p.ID).Scan(&failed).Error; err != nil {
			return err
		}
		if failed >= MaxPinFailures {
			until := s.now().Add(PinLockDuration).Unix()
			lockedUntil = &until
			return tx.Exec("UPDATE profiles SET pin_failed = 0, pin_locked_until = ? WHERE id = ?", until, p.ID).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	if lockedUntil != nil {
		s.logger.Warn().Int64("profileId", p.ID).Msg("profiles: PIN locked after too many failures")
		return &PinLockedError{Until: *lockedUntil}
	}
	return ErrWrongPin
}

//----------------------------------------------------------------------------------------------------------------------
// Sessions
//----------------------------------------------------------------------------------------------------------------------

// ProfileSession is the result of selecting a profile.
type ProfileSession struct {
	ProfileToken string   `json:"profileToken"`
	ExpiresAt    int64    `json:"expiresAt"` // unix seconds
	Profile      *Profile `json:"profile"`
}

// Select checks the PIN (if any) and issues a session token bound to clientID.
func (s *Service) Select(ctx context.Context, id int64, pin string, clientID, platform string) (*ProfileSession, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, ErrNoClientID
	}
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.IsDeleted() {
		return nil, ErrNotFound
	}
	if err := s.checkPin(ctx, p, pin); err != nil {
		return nil, err
	}

	now := s.now()
	expiry := now.Add(s.tokenTTL).Unix()
	if err := s.db.WithContext(ctx).Model(&Profile{}).Where("id = ?", id).Update("last_used_at", now.Unix()).Error; err != nil {
		return nil, err
	}
	s.touchDevice(ctx, clientID, id, platform, true)

	p, err = s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ProfileSession{
		ProfileToken: s.signer.sign(id, clientID, p.TokenVersion, expiry),
		ExpiresAt:    expiry,
		Profile:      p,
	}, nil
}

// Resolve validates a session token for clientID and returns its profile.
func (s *Service) Resolve(ctx context.Context, token, clientID string) (*Profile, error) {
	clientID = strings.TrimSpace(clientID)
	if token == "" || clientID == "" {
		return nil, ErrInvalidToken
	}
	id, expiry, err := s.signer.parse(token)
	if err != nil {
		return nil, err
	}
	p, err := s.Get(ctx, id)
	if err != nil || p.IsDeleted() {
		return nil, ErrInvalidToken
	}
	if err := s.signer.verify(token, id, clientID, p.TokenVersion, expiry, s.now()); err != nil {
		return nil, err
	}
	return p, nil
}

// Logout forgets the device <-> profile link of this client. The token itself is
// stateless; clients drop it and the cookie is cleared by the handler.
func (s *Service) Logout(ctx context.Context, clientID string) error {
	s.touchMu.Lock()
	delete(s.lastTouch, clientID)
	s.touchMu.Unlock()
	return s.db.WithContext(ctx).Exec("DELETE FROM profile_devices WHERE client_id = ?", clientID).Error
}

// TouchDevice records that clientID is using profileID (throttled).
func (s *Service) TouchDevice(ctx context.Context, clientID string, profileID int64, platform string) {
	s.touchDevice(ctx, clientID, profileID, platform, false)
}

func (s *Service) touchDevice(ctx context.Context, clientID string, profileID int64, platform string, force bool) {
	if clientID == "" || profileID == 0 {
		return
	}
	now := s.now()
	key := clientID + "\x00" + strconv.FormatInt(profileID, 10)
	s.touchMu.Lock()
	if last, ok := s.lastTouch[key]; ok && !force && now.Sub(last) < deviceTouchGap {
		s.touchMu.Unlock()
		return
	}
	s.lastTouch[key] = now
	s.touchMu.Unlock()

	var plat *string
	if platform != "" {
		plat = &platform
	}
	err := s.db.WithContext(ctx).Exec(`INSERT INTO profile_devices (client_id, profile_id, platform, last_seen_at) VALUES (?, ?, ?, ?)
ON CONFLICT(client_id) DO UPDATE SET profile_id = excluded.profile_id, platform = COALESCE(excluded.platform, profile_devices.platform), last_seen_at = excluded.last_seen_at`,
		clientID, profileID, plat, now.Unix()).Error
	if err != nil {
		s.logger.Debug().Err(err).Msg("profiles: touch device")
	}
}

// DeviceProfile returns the profile id last selected on clientID (0 if none). For
// event routing only, never to skip the profile selector.
func (s *Service) DeviceProfile(ctx context.Context, clientID string) int64 {
	var id int64
	_ = s.db.WithContext(ctx).Raw("SELECT profile_id FROM profile_devices WHERE client_id = ?", clientID).Scan(&id).Error
	return id
}
