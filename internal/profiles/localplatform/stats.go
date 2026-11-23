package localplatform

import (
	"context"
	"seanime/internal/api/anilist"
	"sort"
)

type statAcc struct {
	count    int
	minutes  int
	scoreSum int
	scored   int
	ids      []*int
}

func (a *statAcc) add(mediaID int, minutes int, score *int) {
	a.count++
	a.minutes += minutes
	if score != nil && *score > 0 {
		a.scoreSum += *score
		a.scored++
	}
	a.ids = append(a.ids, new(mediaID))
}

func (a *statAcc) mean() float64 {
	if a.scored == 0 {
		return 0
	}
	return float64(a.scoreSum) / float64(a.scored)
}

// GetViewerStats aggregates the profile's anime list like AniList's user
// statistics (scores in POINT_100). Studios aren't in the snapshots: empty.
func (p *Platform) GetViewerStats(ctx context.Context) (*anilist.ViewerStats, error) {
	// Per-status counts straight from SQL.
	type statusRow struct {
		Status   string `gorm:"column:status"`
		Count    int    `gorm:"column:n"`
		ScoreSum int    `gorm:"column:score_sum"`
		Scored   int    `gorm:"column:scored"`
	}
	var statusRows []statusRow
	if err := p.db.WithContext(ctx).Raw(`SELECT status, COUNT(*) AS n, COALESCE(SUM(score_raw), 0) AS score_sum, COUNT(score_raw) AS scored
FROM list_entries WHERE profile_id = ? AND media_type = ? GROUP BY status ORDER BY status`, p.profileID, mediaTypeAnime).Scan(&statusRows).Error; err != nil {
		return nil, err
	}

	rows, err := p.Entries(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.MediaID)
	}
	media, err := p.meta.GetBaseAnimeMany(ctx, ids)
	if err != nil {
		media = map[int]*anilist.BaseAnime{}
	}

	stats := &anilist.ViewerStats_Viewer_Statistics_Anime{}
	statusAcc := map[string]*statAcc{}
	formats := map[anilist.MediaFormat]*statAcc{}
	genres := map[string]*statAcc{}
	scores := map[int]*statAcc{}
	startYears := map[int]*statAcc{}
	releaseYears := map[int]*statAcc{}
	total := &statAcc{}
	get := func(m map[int]*statAcc, k int) *statAcc {
		if m[k] == nil {
			m[k] = &statAcc{}
		}
		return m[k]
	}

	for i := range rows {
		row := &rows[i]
		if row.Status == string(anilist.MediaListStatusPlanning) {
			if statusAcc[row.Status] == nil {
				statusAcc[row.Status] = &statAcc{}
			}
			statusAcc[row.Status].add(row.MediaID, 0, row.ScoreRaw)
			continue // AniList doesn't count planned media in the totals
		}
		m := media[row.MediaID]
		duration, episodes := 0, 0
		if m != nil {
			if m.Duration != nil {
				duration = *m.Duration
			}
			if m.Episodes != nil {
				episodes = *m.Episodes
			}
		}
		watched := row.Progress + row.Repeat*episodes
		minutes := watched * duration
		stats.EpisodesWatched += watched
		total.add(row.MediaID, minutes, row.ScoreRaw)

		if statusAcc[row.Status] == nil {
			statusAcc[row.Status] = &statAcc{}
		}
		statusAcc[row.Status].add(row.MediaID, minutes, row.ScoreRaw)
		if row.ScoreRaw != nil && *row.ScoreRaw > 0 {
			get(scores, (*row.ScoreRaw+9)/10*10).add(row.MediaID, minutes, row.ScoreRaw)
		}
		if row.StartedY != nil {
			get(startYears, *row.StartedY).add(row.MediaID, minutes, row.ScoreRaw)
		}
		if m == nil {
			continue
		}
		if m.Format != nil {
			if formats[*m.Format] == nil {
				formats[*m.Format] = &statAcc{}
			}
			formats[*m.Format].add(row.MediaID, minutes, row.ScoreRaw)
		}
		for _, g := range m.Genres {
			if g == nil {
				continue
			}
			if genres[*g] == nil {
				genres[*g] = &statAcc{}
			}
			genres[*g].add(row.MediaID, minutes, row.ScoreRaw)
		}
		if m.StartDate != nil && m.StartDate.Year != nil {
			get(releaseYears, *m.StartDate.Year).add(row.MediaID, minutes, row.ScoreRaw)
		}
	}

	stats.Count = total.count
	stats.MinutesWatched = total.minutes
	stats.MeanScore = total.mean()

	for _, sr := range statusRows {
		acc := statusAcc[sr.Status]
		if acc == nil {
			acc = &statAcc{}
		}
		st := anilist.MediaListStatus(sr.Status)
		mean := 0.0
		if sr.Scored > 0 {
			mean = float64(sr.ScoreSum) / float64(sr.Scored)
		}
		stats.Statuses = append(stats.Statuses, &anilist.UserStatusStats{Status: &st, Count: sr.Count, MeanScore: mean,
			MinutesWatched: acc.minutes, MediaIds: acc.ids})
	}
	for f, acc := range formats {
		stats.Formats = append(stats.Formats, &anilist.UserFormatStats{Format: new(f), Count: acc.count, MeanScore: acc.mean(), MinutesWatched: acc.minutes, MediaIds: acc.ids})
	}
	sort.Slice(stats.Formats, func(i, j int) bool { return stats.Formats[i].Count > stats.Formats[j].Count })
	for g, acc := range genres {
		stats.Genres = append(stats.Genres, &anilist.UserGenreStats{Genre: new(g), Count: acc.count, MeanScore: acc.mean(), MinutesWatched: acc.minutes, MediaIds: acc.ids})
	}
	sort.Slice(stats.Genres, func(i, j int) bool {
		if stats.Genres[i].Count != stats.Genres[j].Count {
			return stats.Genres[i].Count > stats.Genres[j].Count
		}
		return *stats.Genres[i].Genre < *stats.Genres[j].Genre
	})
	for _, k := range sortedKeys(scores) {
		acc := scores[k]
		stats.Scores = append(stats.Scores, &anilist.UserScoreStats{Score: new(k), Count: acc.count, MeanScore: acc.mean(), MinutesWatched: acc.minutes, MediaIds: acc.ids})
	}
	for _, k := range sortedKeys(startYears) {
		acc := startYears[k]
		stats.StartYears = append(stats.StartYears, &anilist.UserStartYearStats{StartYear: new(k), Count: acc.count, MeanScore: acc.mean(), MinutesWatched: acc.minutes, MediaIds: acc.ids})
	}
	for _, k := range sortedKeys(releaseYears) {
		acc := releaseYears[k]
		stats.ReleaseYears = append(stats.ReleaseYears, &anilist.UserReleaseYearStats{ReleaseYear: new(k), Count: acc.count, MeanScore: acc.mean(), MinutesWatched: acc.minutes, MediaIds: acc.ids})
	}
	stats.Studios = []*anilist.UserStudioStats{}

	return &anilist.ViewerStats{Viewer: &anilist.ViewerStats_Viewer{Statistics: &anilist.ViewerStats_Viewer_Statistics{
		Anime: stats,
		Manga: &anilist.ViewerStats_Viewer_Statistics_Manga{},
	}}}, nil
}

func sortedKeys(m map[int]*statAcc) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
