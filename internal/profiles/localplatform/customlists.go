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

// ErrDuplicateListName: another custom list of the profile has that name.
var ErrDuplicateListName = errors.New("a custom list with that name already exists")

// UpdateCustomList renames a custom list and/or moves it to another position
// (0-based; the other lists are renumbered). Nil fields are unchanged.
func (p *Platform) UpdateCustomList(ctx context.Context, listID int64, name *string, position *int) error {
	if name != nil {
		n, err := normalizeListName(*name)
		if err != nil {
			return err
		}
		name = &n
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lists []CustomListRow
		if err := tx.Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime).Order("position, id").Find(&lists).Error; err != nil {
			return err
		}
		idx := -1
		for i, l := range lists {
			if l.ID == listID {
				idx = i
			}
		}
		if idx < 0 {
			return ErrCustomListNotFound
		}
		if name != nil && *name != lists[idx].Name {
			for i, l := range lists {
				if i != idx && strings.EqualFold(l.Name, *name) {
					return ErrDuplicateListName
				}
			}
			if err := tx.Model(&CustomListRow{}).Where("id = ? AND profile_id = ?", listID, p.profileID).Update("name", *name).Error; err != nil {
				return err
			}
		}
		if position != nil {
			target := min(max(*position, 0), len(lists)-1)
			moved := lists[idx]
			lists = append(lists[:idx], lists[idx+1:]...)
			lists = append(lists[:target], append([]CustomListRow{moved}, lists[target:]...)...)
			for i, l := range lists {
				if l.Position == i {
					continue
				}
				if err := tx.Model(&CustomListRow{}).Where("id = ? AND profile_id = ?", l.ID, p.profileID).Update("position", i).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	p.Invalidate()
	return nil
}

// EntryCustomListIDs returns the ids of the custom lists that hold the media.
func (p *Platform) EntryCustomListIDs(ctx context.Context, mediaID int) ([]int64, error) {
	ids := make([]int64, 0)
	err := p.db.WithContext(ctx).Raw(`SELECT i.list_id FROM custom_list_items i
JOIN custom_lists l ON l.id = i.list_id
JOIN list_entries e ON e.id = i.entry_id
WHERE l.profile_id = ? AND e.profile_id = ? AND e.media_id = ? ORDER BY l.position, l.id`, p.profileID, p.profileID, mediaID).Scan(&ids).Error
	return ids, err
}

// setEntryCustomListsTx makes the media's entry belong to exactly listIDs.
func (p *Platform) setEntryCustomListsTx(ctx context.Context, tx *gorm.DB, mediaID int, listIDs []int64) error {
	entry, err := p.findEntry(ctx, tx, mediaID)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, id := range listIDs {
		want[id] = true
	}
	var lists []CustomListRow
	if err := tx.Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime).Find(&lists).Error; err != nil {
		return err
	}
	owned := map[int64]bool{}
	for _, l := range lists {
		owned[l.ID] = true
	}
	for id := range want {
		if !owned[id] {
			return ErrCustomListNotFound
		}
	}
	for _, l := range lists {
		if want[l.ID] {
			if err := tx.Exec("INSERT OR IGNORE INTO custom_list_items (list_id, entry_id, added_at) VALUES (?, ?, ?)", l.ID, entry.ID, p.now().Unix()).Error; err != nil {
				return err
			}
		} else if err := tx.Exec("DELETE FROM custom_list_items WHERE list_id = ? AND entry_id = ?", l.ID, entry.ID).Error; err != nil {
			return err
		}
	}
	return nil
}
