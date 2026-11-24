package autoselect

// Garceslab Anime: subtitle-aware debrid auto-select.
//
// Release names rarely say which subtitle languages a file has (a Crunchyroll
// WEB release has 8-10 tracks and says "MultiSub" at most), so after the
// name-based choice, the MKV headers of a few cached candidates are read over
// HTTP Range (see internal/torrents/trackprobe) and a release that really has
// the profile's preferred subtitle (or dub) language is preferred.
//
// Selection rule:
//   - Candidates: the name-based choice, then the next cached torrents in the
//     ranked list with the same resolution as the choice, up to TopN in all.
//     A lower resolution is never picked for subtitles.
//   - They are probed in rank order. A candidate scores 2 if it has a preferred
//     dub language (audio languages listed before Japanese), 1 if it has a
//     full (not forced, not "Signs & Songs") subtitle track in a preferred
//     language, 0 otherwise. The best score wins, ties go to the higher rank,
//     so the name-based choice stays unless a candidate is strictly better.
//   - Probing stops at the first candidate with the best possible score, on a
//     429 (from the debrid API or the file host), or when the budget is spent;
//     the best candidate found so far is used (the name-based choice if none).
//   - The name-based choice must itself be cached; otherwise nothing is probed.
//   - No preference = no probing.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"seanime/internal/api/anilist"
	"seanime/internal/debrid/debrid"
	hibiketorrent "seanime/internal/extension/hibike/torrent"
	"seanime/internal/torrents/trackprobe"
	"strings"
	"time"

	"github.com/5rahim/habari"
)

const (
	DefaultProbeTopN         = 4
	DefaultProbeBudget       = 10 * time.Second
	DefaultProbePerCandidate = 5 * time.Second
	DefaultProbePace         = 500 * time.Millisecond
)

type (
	// TrackPreference is what the requesting profile wants to hear and read.
	TrackPreference struct {
		Subtitles     []string // canonical language keys, in order
		Dub           []string // audio languages preferred over Japanese
		IgnoredLabels []string // lowercased subtitle name parts that don't count ("signs")
	}

	// TrackProbeOptions turn on the subtitle-aware step of a debrid auto-select.
	TrackProbeOptions struct {
		Preference   TrackPreference
		Linker       debrid.CachedFileLinker
		ProviderID   string
		Cache        *trackprobe.Cache
		TopN         int
		Budget       time.Duration
		PerCandidate time.Duration
		Pace         time.Duration
		// Probe reads the tracks of a link (default: trackprobe.ProbeURL).
		Probe func(ctx context.Context, link string) (*trackprobe.Tracks, error)
		// OnProgress also receives the progress text (e.g. for the TV's
		// loading screen); it must route to the requesting client only.
		OnProgress func(detail string)

		// resolve finds the episode file of a candidate (tests).
		resolve func(ctx context.Context, t *hibiketorrent.AnimeTorrent) (*Result, error)
	}

	// FindOption is an optional argument of FindBestTorrent.
	FindOption func(*findConfig)

	findConfig struct {
		trackProbe *TrackProbeOptions
	}
)

// WithTrackProbe enables the subtitle-aware step (debrid mode only).
func WithTrackProbe(o *TrackProbeOptions) FindOption {
	return func(c *findConfig) { c.trackProbe = o }
}

// NewTrackPreference builds a preference from the TV player's comma-separated
// settings ("spa, es, spanish").
func NewTrackPreference(subtitles, audio, ignoredLabels string) TrackPreference {
	p := TrackPreference{Subtitles: trackprobe.ParseList(subtitles)}
	for _, l := range trackprobe.ParseList(audio) {
		if l == "ja" {
			break
		}
		p.Dub = append(p.Dub, l)
	}
	if strings.TrimSpace(ignoredLabels) == "" {
		ignoredLabels = "signs, songs" // the TV's default
	}
	for _, part := range strings.Split(ignoredLabels, ",") {
		if v := strings.ToLower(strings.TrimSpace(part)); v != "" {
			p.IgnoredLabels = append(p.IgnoredLabels, v)
		}
	}
	return p
}

// IsEmpty tells whether there is nothing to look for.
func (p TrackPreference) IsEmpty() bool {
	return len(p.Subtitles) == 0 && len(p.Dub) == 0
}

func (p TrackPreference) maxScore() int {
	if len(p.Dub) > 0 {
		return 2
	}
	return 1
}

// score: -1 unknown, 0 nothing preferred, 1 preferred subtitles, 2 preferred dub.
func (p TrackPreference) score(tr *trackprobe.Tracks) int {
	if tr == nil || tr.NotMatroska {
		return -1
	}
	if len(p.Dub) > 0 && p.hasAudio(tr) {
		return 2
	}
	if len(p.Subtitles) > 0 && p.hasSubtitles(tr) {
		return 1
	}
	return 0
}

func (p TrackPreference) hasAudio(tr *trackprobe.Tracks) bool {
	for _, t := range tr.Audio {
		if anyIn(t.Languages, p.Dub) {
			return true
		}
	}
	return false
}

func (p TrackPreference) hasSubtitles(tr *trackprobe.Tracks) bool {
	for _, t := range tr.Subtitles {
		if t.Forced || p.ignored(t.Name) {
			continue
		}
		if anyIn(t.Languages, p.Subtitles) {
			return true
		}
	}
	return false
}

func (p TrackPreference) ignored(name string) bool {
	lower := strings.ToLower(name)
	for _, l := range p.IgnoredLabels {
		if lower != "" && strings.Contains(lower, l) {
			return true
		}
	}
	return false
}

func anyIn(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}

// label is the short text for logs: "spa subs", "spa dub".
func (p TrackPreference) label() string {
	if len(p.Dub) > 0 {
		return trackprobe.Code3(p.Dub[0]) + " dub"
	}
	return trackprobe.Code3(p.Subtitles[0]) + " subs"
}

func describeTracks(tr *trackprobe.Tracks, err error) string {
	switch {
	case err != nil:
		return "unknown"
	case tr == nil || tr.NotMatroska:
		return "not mkv"
	}
	var codes []string
	seen := map[string]bool{}
	for _, t := range tr.Subtitles {
		for _, l := range t.Languages {
			if !seen[l] {
				seen[l] = true
				codes = append(codes, trackprobe.Code3(l))
			}
		}
	}
	if len(codes) == 0 {
		return "no subs"
	}
	return strings.Join(codes, ", ") + " only"
}

type probedCandidate struct {
	torrent *hibiketorrent.AnimeTorrent
	res     *Result
	tracks  *trackprobe.Tracks
	err     error
	score   int
	link    *debrid.CachedFileLink
}

// preferTracks runs the subtitle-aware step on top of the name-based choice.
func (s *AutoSelect) preferTracks(
	ctx context.Context,
	media *anilist.CompleteAnime,
	episodeNumber int,
	ranked []*hibiketorrent.AnimeTorrent,
	chosen *Result,
	cached map[string]bool,
	o *TrackProbeOptions,
	debridClient debrid.Provider,
) *Result {
	if o == nil || chosen == nil || chosen.OriginalTorrent == nil || o.Preference.IsEmpty() || o.Linker == nil {
		return chosen
	}
	topN := o.TopN
	if topN <= 0 {
		return chosen
	}
	winner := chosen.OriginalTorrent
	if !cached[winner.InfoHash] {
		s.logger.Debug().Msg("autoselect: Name-based choice is not cached, not checking subtitle languages")
		return chosen
	}

	// Candidates: the choice, then same-resolution cached torrents ranked below it.
	winnerIdx := -1
	for i, t := range ranked {
		if t == winner {
			winnerIdx = i
			break
		}
	}
	if winnerIdx < 0 {
		return chosen
	}
	res := strings.ToLower(habari.Parse(winner.Name).VideoResolution)
	list := []*hibiketorrent.AnimeTorrent{winner}
	for _, t := range ranked[winnerIdx+1:] {
		if len(list) >= topN {
			break
		}
		if !cached[t.InfoHash] || t.InfoHash == "" {
			continue
		}
		if strings.ToLower(habari.Parse(t.Name).VideoResolution) != res {
			continue
		}
		list = append(list, t)
	}

	budget := durationOr(o.Budget, DefaultProbeBudget)
	perCandidate := durationOr(o.PerCandidate, DefaultProbePerCandidate)
	pace := durationOr(o.Pace, DefaultProbePace)
	probe := o.Probe
	if probe == nil {
		probe = func(ctx context.Context, link string) (*trackprobe.Tracks, error) {
			tr, _, err := trackprobe.ProbeURL(ctx, link, trackprobe.Options{})
			return tr, err
		}
	}
	resolve := o.resolve
	if resolve == nil {
		resolve = func(ctx context.Context, t *hibiketorrent.AnimeTorrent) (*Result, error) {
			ext, ok := s.torrentRepository.GetAnimeProviderExtension(t.Provider)
			if !ok {
				return nil, fmt.Errorf("provider not found")
			}
			return s.selectFileFromDebrid(ctx, media, episodeNumber, t, ext, debridClient)
		}
	}

	bctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	what := "subtitle"
	if len(o.Preference.Subtitles) == 0 {
		what = "audio"
	}
	var probed []*probedCandidate
	var lastNetwork time.Time
	stopReason := ""

	for i, t := range list {
		if bctx.Err() != nil {
			stopReason = "budget spent"
			break
		}
		detail := fmt.Sprintf("Checking %s languages… (%d/%d)", what, i+1, len(list))
		s.updateStep(ctx, "probing", detail)
		if o.OnProgress != nil {
			o.OnProgress(detail)
		}
		s.updateCandidateStatus(ctx, t.Name, "analyzing")

		pc := &probedCandidate{torrent: t, score: -1}
		if i == 0 {
			pc.res = chosen
		} else {
			r, err := resolveWithin(bctx, resolve, t)
			if err != nil && (isRateLimited(err) || bctx.Err() != nil) {
				s.updateCandidateStatus(ctx, t.Name, "waiting")
				stopReason = "rate limited"
				if !isRateLimited(err) {
					stopReason = "budget spent"
				}
				break
			}
			if err != nil || r == nil || r.DebridTorrent == nil {
				s.logger.Debug().Str("name", t.Name).Msg("autoselect: Could not find the episode file of a probe candidate")
				s.updateCandidateStatus(ctx, t.Name, "skipped")
				continue
			}
			pc.res = r
		}

		filePath := debridFilePath(pc.res)
		key := trackprobe.Key(o.ProviderID, t.InfoHash, filePath)
		if tr, ok := o.Cache.Get(key); ok {
			pc.tracks = tr
		} else if !strings.EqualFold(path.Ext(filePath), ".mkv") {
			pc.tracks = &trackprobe.Tracks{NotMatroska: true}
		} else {
			if !lastNetwork.IsZero() {
				if wait := pace - time.Since(lastNetwork); wait > 0 {
					select {
					case <-bctx.Done():
					case <-time.After(wait):
					}
					if bctx.Err() != nil {
						stopReason = "budget spent"
						if i > 0 {
							s.updateCandidateStatus(ctx, t.Name, "waiting")
						}
						break
					}
				}
			}
			lastNetwork = time.Now()
			pc.tracks, pc.link, pc.err = s.probeOne(bctx, perCandidate, o, probe, t, pc.res)
			if pc.err == nil && pc.tracks != nil {
				if err := o.Cache.Put(key, pc.tracks); err != nil {
					s.logger.Debug().Err(err).Msg("autoselect: Could not cache probe result")
				}
			}
		}
		pc.score = o.Preference.score(pc.tracks)
		probed = append(probed, pc)

		if i > 0 {
			s.updateCandidateStatus(ctx, t.Name, "waiting")
		}
		if pc.err != nil {
			if isRateLimited(pc.err) {
				stopReason = "rate limited"
				break
			}
			s.logger.Debug().Str("name", t.Name).Str("error", pc.err.Error()).Msg("autoselect: Could not read the tracks of a candidate")
		}
		if pc.score >= o.Preference.maxScore() {
			break
		}
	}

	// Best score, ties to the higher rank: the name-based choice (probed[0])
	// stays unless a candidate is strictly better.
	result := chosen
	best, bestScore := -1, 0
	if len(probed) > 0 {
		best = 0
		if probed[0].score > 0 {
			bestScore = probed[0].score
		}
		for i, pc := range probed[1:] {
			if pc.score > bestScore {
				best, bestScore = i+1, pc.score
			}
		}
		result = probed[best].res
	}
	for i, pc := range probed {
		if pc.link != nil && pc.link.Release != nil {
			pc.link.Release(i == best)
		}
	}

	if stopReason != "" {
		s.logger.Info().Msgf("autoselect: Stopped checking track languages (%s)", stopReason)
	}
	winnerDesc := "unknown"
	if len(probed) > 0 && probed[0].torrent == winner {
		winnerDesc = describeTracks(probed[0].tracks, probed[0].err)
	}
	switch {
	case result != chosen:
		s.logger.Info().Msgf("autoselect: Picked %s (has %s) over %s (%s)",
			result.OriginalTorrent.Name, o.Preference.label(), winner.Name, winnerDesc)
		s.log(fmt.Sprintf("Picked %s (has %s)", result.OriginalTorrent.Name, o.Preference.label()))
		s.updateCandidateStatus(ctx, winner.Name, "skipped")
		s.updateCandidateStatus(ctx, result.OriginalTorrent.Name, "selected")
	case bestScore > 0:
		s.logger.Info().Msgf("autoselect: Kept %s (has %s)", winner.Name, o.Preference.label())
		s.updateCandidateStatus(ctx, winner.Name, "selected")
	default:
		s.logger.Info().Msgf("autoselect: No checked candidate has %s (%d checked), keeping %s (%s)",
			o.Preference.label(), len(probed), winner.Name, winnerDesc)
		s.updateCandidateStatus(ctx, winner.Name, "selected")
	}
	return result
}

// resolveWithin runs resolve but gives up when ctx ends (the debrid file
// lookup doesn't take a context).
func resolveWithin(ctx context.Context, resolve func(context.Context, *hibiketorrent.AnimeTorrent) (*Result, error), t *hibiketorrent.AnimeTorrent) (*Result, error) {
	type out struct {
		r   *Result
		err error
	}
	ch := make(chan out, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- out{err: fmt.Errorf("resolve panicked")}
			}
		}()
		r, err := resolve(ctx, t)
		ch <- out{r, err}
	}()
	select {
	case o := <-ch:
		return o.r, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// probeOne gets a link to the file and reads its header.
func (s *AutoSelect) probeOne(
	ctx context.Context,
	timeout time.Duration,
	o *TrackProbeOptions,
	probe func(ctx context.Context, link string) (*trackprobe.Tracks, error),
	t *hibiketorrent.AnimeTorrent,
	res *Result,
) (*trackprobe.Tracks, *debrid.CachedFileLink, error) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	link, err := o.Linker.CachedFileLink(pctx, debrid.CachedFileLinkOptions{
		MagnetLink: t.MagnetLink,
		InfoHash:   t.InfoHash,
		FileId:     res.DebridFileID,
	})
	if err != nil {
		return nil, nil, err
	}
	tr, err := probe(pctx, link.URL)
	return tr, link, err
}

func debridFilePath(res *Result) string {
	if res == nil || res.DebridTorrent == nil {
		return ""
	}
	for _, f := range res.DebridTorrent.Files {
		if f.ID == res.DebridFileID {
			if f.Path != "" {
				return f.Path
			}
			return f.Name
		}
	}
	return res.DebridFileID
}

func isRateLimited(err error) bool {
	return errors.Is(err, debrid.ErrRateLimited) || errors.Is(err, trackprobe.ErrRateLimited)
}

func durationOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
