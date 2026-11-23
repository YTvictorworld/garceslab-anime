package localplatform

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

var (
	ErrCustomListNotFound = errors.New("custom list not found")
	ErrInvalidListName    = errors.New("invalid custom list name")
)

// ProfileCustomList is a custom list with the media ids it holds.
type ProfileCustomList struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	MediaIDs []int  `json:"mediaIds"`
}

func normalizeListName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 50 {
		return "", ErrInvalidListName
	}
	return name, nil
}

// CustomLists returns the profile's anime custom lists.
func (p *Platform) CustomLists(ctx context.Context) ([]*ProfileCustomList, error) {
	var lists []CustomListRow
	if err := p.db.WithContext(ctx).Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime).
		Order("position, id").Find(&lists).Error; err != nil {
		return nil, err
	}
	type item struct {
		ListID  int64 `gorm:"column:list_id"`
		MediaID int   `gorm:"column:media_id"`
	}
	var items []item
	if err := p.db.WithContext(ctx).Raw(`SELECT i.list_id, e.media_id FROM custom_list_items i
JOIN custom_lists l ON l.id = i.list_id
JOIN list_entries e ON e.id = i.entry_id
WHERE l.profile_id = ? AND e.profile_id = ? ORDER BY i.added_at, e.media_id`, p.profileID, p.profileID).Scan(&items).Error; err != nil {
		return nil, err
	}
	byList := map[int64][]int{}
	for _, it := range items {
		byList[it.ListID] = append(byList[it.ListID], it.MediaID)
	}
	ret := make([]*ProfileCustomList, 0, len(lists))
	for _, l := range lists {
		ids := byList[l.ID]
		if ids == nil {
			ids = []int{}
		}
		ret = append(ret, &ProfileCustomList{ID: l.ID, Name: l.Name, Position: l.Position, MediaIDs: ids})
	}
	return ret, nil
}

// CreateCustomList creates (or returns the existing) custom list with that name.
func (p *Platform) CreateCustomList(ctx context.Context, name string) (int64, error) {
	name, err := normalizeListName(name)
	if err != nil {
		return 0, err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	var id int64
	err = p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = p.createCustomListTx(tx, name)
		return err
	})
	if err != nil {
		return 0, err
	}
	p.Invalidate()
	return id, nil
}

func (p *Platform) createCustomListTx(tx *gorm.DB, name string) (int64, error) {
	var existing []CustomListRow
	if err := tx.Where("profile_id = ? AND media_type = ? AND name = ?", p.profileID, mediaTypeAnime, name).Limit(1).Find(&existing).Error; err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return existing[0].ID, nil
	}
	var pos int
	if err := tx.Raw("SELECT COALESCE(MAX(position), -1) + 1 FROM custom_lists WHERE profile_id = ?", p.profileID).Scan(&pos).Error; err != nil {
		return 0, err
	}
	row := &CustomListRow{ProfileID: p.profileID, MediaType: mediaTypeAnime, Name: name, Position: pos, CreatedAt: p.now().Unix()}
	if err := tx.Create(row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// DeleteCustomList deletes one of the profile's custom lists (the entries stay).
func (p *Platform) DeleteCustomList(ctx context.Context, listID int64) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	res := p.db.WithContext(ctx).Where("id = ? AND profile_id = ?", listID, p.profileID).Delete(&CustomListRow{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCustomListNotFound
	}
	p.Invalidate()
	return nil
}

// SetInCustomList adds or removes a media (which must be in the list) from a custom list.
func (p *Platform) SetInCustomList(ctx context.Context, listID int64, mediaID int, in bool) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return p.setInCustomListTx(ctx, tx, listID, mediaID, in)
	})
	if err != nil {
		return err
	}
	p.Invalidate()
	return nil
}

func (p *Platform) setInCustomListTx(ctx context.Context, tx *gorm.DB, listID int64, mediaID int, in bool) error {
	var lists []CustomListRow
	if err := tx.Where("id = ? AND profile_id = ?", listID, p.profileID).Limit(1).Find(&lists).Error; err != nil {
		return err
	}
	if len(lists) == 0 {
		return ErrCustomListNotFound
	}
	entry, err := p.findEntry(ctx, tx, mediaID)
	if err != nil {
		return err
	}
	if in {
		return tx.Exec("INSERT OR IGNORE INTO custom_list_items (list_id, entry_id, added_at) VALUES (?, ?, ?)",
			listID, entry.ID, p.now().Unix()).Error
	}
	return tx.Exec("DELETE FROM custom_list_items WHERE list_id = ? AND entry_id = ?", listID, entry.ID).Error
}
