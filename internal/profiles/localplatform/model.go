package localplatform

import (
	"fmt"
	"math"
	"seanime/internal/api/anilist"
	"strconv"
	"time"
)

// EntryRow is a row of list_entries. Every query on it filters by profile_id.
type EntryRow struct {
	ID                    int64   `gorm:"column:id;primaryKey" json:"id"`
	ProfileID             int64   `gorm:"column:profile_id" json:"-"`
	MediaType             string  `gorm:"column:media_type" json:"mediaType"`
	MediaID               int     `gorm:"column:media_id" json:"mediaId"`
	Status                string  `gorm:"column:status" json:"status"`
	Progress              int     `gorm:"column:progress" json:"progress"`
	ProgressVolumes       *int    `gorm:"column:progress_volumes" json:"progressVolumes,omitempty"`
	ScoreRaw              *int    `gorm:"column:score_raw" json:"scoreRaw,omitempty"`
	Repeat                int     `gorm:"column:repeat" json:"repeat"`
	Private               bool    `gorm:"column:private" json:"private"`
	HiddenFromStatusLists bool    `gorm:"column:hidden_from_status_lists" json:"hiddenFromStatusLists"`
	Notes                 *string `gorm:"column:notes" json:"notes,omitempty"`
	StartedY              *int    `gorm:"column:started_y" json:"-"`
	StartedM              *int    `gorm:"column:started_m" json:"-"`
	StartedD              *int    `gorm:"column:started_d" json:"-"`
	CompletedY            *int    `gorm:"column:completed_y" json:"-"`
	CompletedM            *int    `gorm:"column:completed_m" json:"-"`
	CompletedD            *int    `gorm:"column:completed_d" json:"-"`
	CreatedAt             int64   `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt             int64   `gorm:"column:updated_at" json:"updatedAt"`
}

func (EntryRow) TableName() string { return "list_entries" }

// ProfileFuzzyDate is AniList's partial date (any part may be missing).
type ProfileFuzzyDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

func (d ProfileFuzzyDate) IsZero() bool { return d.Year == nil && d.Month == nil && d.Day == nil }

func (r *EntryRow) StartedAt() ProfileFuzzyDate {
	return ProfileFuzzyDate{Year: r.StartedY, Month: r.StartedM, Day: r.StartedD}
}

func (r *EntryRow) CompletedAt() ProfileFuzzyDate {
	return ProfileFuzzyDate{Year: r.CompletedY, Month: r.CompletedM, Day: r.CompletedD}
}

func (r *EntryRow) setStartedAt(d ProfileFuzzyDate) {
	r.StartedY, r.StartedM, r.StartedD = cleanDate(d)
}

func (r *EntryRow) setCompletedAt(d ProfileFuzzyDate) {
	r.CompletedY, r.CompletedM, r.CompletedD = cleanDate(d)
}

// cleanDate drops invalid parts: a month needs a year, a day needs a month.
func cleanDate(d ProfileFuzzyDate) (y, m, day *int) {
	if d.Year != nil && *d.Year > 0 && *d.Year < 10000 {
		y = new(*d.Year)
	}
	if y != nil && d.Month != nil && *d.Month >= 1 && *d.Month <= 12 {
		m = new(*d.Month)
	}
	if m != nil && d.Day != nil && *d.Day >= 1 && *d.Day <= 31 {
		day = new(*d.Day)
	}
	return
}

func fromInput(in *anilist.FuzzyDateInput) ProfileFuzzyDate {
	if in == nil {
		return ProfileFuzzyDate{}
	}
	return ProfileFuzzyDate{Year: in.Year, Month: in.Month, Day: in.Day}
}

func dateString(d ProfileFuzzyDate) string {
	y, m, day := cleanDate(d)
	switch {
	case y == nil:
		return ""
	case m == nil:
		return itoa(*y)
	case day == nil:
		return itoa(*y) + "-" + pad2(*m)
	default:
		return itoa(*y) + "-" + pad2(*m) + "-" + pad2(*day)
	}
}

// Today returns the local date as a ProfileFuzzyDate.
func Today(now time.Time) ProfileFuzzyDate {
	return ProfileFuzzyDate{Year: new(now.Year()), Month: new(int(now.Month())), Day: new(now.Day())}
}

//----------------------------------------------------------------------------------------------------------------------
// Scores
//
// list_entries.score_raw is AniList's scoreRaw (0-100, NULL = no score). The anime
// collection exposes it as `score(format: POINT_100)`, like the AniList query
// (queries/anime.graphql), so the clients convert it to the profile's format.
// ScoreForFormat / ScoreRawFromFormat do the same conversion server side (stats,
// export, imports in another format).

const (
	ScorePoint100      = "POINT_100"
	ScorePoint10Dec    = "POINT_10_DECIMAL"
	ScorePoint10       = "POINT_10"
	ScorePoint5        = "POINT_5"
	ScorePoint3        = "POINT_3"
	DefaultScoreFormat = ScorePoint10Dec
)

// ScoreForFormat converts a raw 0-100 score to the given AniList score format.
func ScoreForFormat(raw int, format string) float64 {
	if raw <= 0 {
		return 0
	}
	raw = min(raw, 100)
	switch format {
	case ScorePoint100:
		return float64(raw)
	case ScorePoint10:
		return math.Round(float64(raw) / 10)
	case ScorePoint5:
		return math.Round(float64(raw) / 20)
	case ScorePoint3:
		switch {
		case raw <= 35:
			return 1
		case raw <= 60:
			return 2
		default:
			return 3
		}
	default: // POINT_10_DECIMAL
		return math.Round(float64(raw)) / 10
	}
}

// ScoreRawFromFormat converts a score in the given format to raw 0-100.
func ScoreRawFromFormat(score float64, format string) int {
	if score <= 0 {
		return 0
	}
	var raw float64
	switch format {
	case ScorePoint100:
		raw = score
	case ScorePoint10, ScorePoint10Dec:
		raw = score * 10
	case ScorePoint5:
		raw = score * 20
	case ScorePoint3:
		switch {
		case score <= 1:
			raw = 35
		case score <= 2:
			raw = 60
		default:
			raw = 85
		}
	default:
		raw = score * 10
	}
	return int(math.Max(0, math.Min(100, math.Round(raw))))
}

func normalizeScoreRaw(v *int) *int {
	if v == nil || *v <= 0 {
		return nil
	}
	return new(min(*v, 100))
}

func itoa(v int) string { return strconv.Itoa(v) }

func pad2(v int) string { return fmt.Sprintf("%02d", v) }
