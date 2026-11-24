package trackprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"seanime/internal/matroska"
	"strings"
)

const (
	// DefaultMaxBytes is the most a probe reads from one file.
	DefaultMaxBytes int64 = 2 << 20
	// DefaultChunkBytes is the size of one Range request.
	DefaultChunkBytes int64 = 64 << 10

	maxEBMLHeaderBytes = 4 << 10
	maxSeekHeadBytes   = 64 << 10
	maxTopLevelSteps   = 64
)

// ErrTracksNotFound is returned when no Tracks element is found before the
// first Cluster (and the SeekHead doesn't point to one).
var ErrTracksNotFound = errors.New("trackprobe: no tracks element in the header")

type (
	// Track is one audio or subtitle track.
	Track struct {
		Languages []string `json:"languages,omitempty"` // canonical keys, see Normalize
		Name      string   `json:"name,omitempty"`
		Forced    bool     `json:"forced,omitempty"`
	}

	// Tracks is the result of a probe.
	Tracks struct {
		Audio     []Track `json:"audio,omitempty"`
		Subtitles []Track `json:"subtitles,omitempty"`
		// NotMatroska: the file isn't an MKV (e.g. MP4), so its tracks are unknown.
		NotMatroska bool `json:"notMatroska,omitempty"`
	}

	// Options for ProbeURL. Zero values use the defaults.
	Options struct {
		Client     *http.Client
		MaxBytes   int64
		ChunkBytes int64
	}

	// Stats tell how much a probe fetched.
	Stats struct {
		Requests int
		Bytes    int64
	}
)

// ProbeURL reads the track list of the MKV file at link with Range requests.
// The returned error never contains the link.
func ProbeURL(ctx context.Context, link string, opts Options) (*Tracks, Stats, error) {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.ChunkBytes <= 0 {
		opts.ChunkBytes = DefaultChunkBytes
	}
	rr := newRangeReader(ctx, opts.Client, link, opts.ChunkBytes, opts.MaxBytes)
	tracks, err := ProbeReader(rr)
	return tracks, Stats{Requests: rr.requests, Bytes: rr.fetched}, err
}

// ProbeReader reads the track list of an MKV file: EBML header, Segment, then
// the top-level elements up to Tracks, skipping (not reading) everything else
// and following the SeekHead when Tracks comes later in the file.
func ProbeReader(rs io.ReadSeeker) (*Tracks, error) {
	er := matroska.NewEBMLReader(rs)
	if _, err := er.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	id, size, err := er.ReadElementHeader()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return &Tracks{NotMatroska: true}, nil
		}
		return nil, err
	}
	if id != matroska.IDEBMLHeader || size > maxEBMLHeaderBytes {
		return &Tracks{NotMatroska: true}, nil
	}
	headerEnd := er.Position() + int64(size)
	ebmlRaw, err := readAt(er, rs, 0, headerEnd)
	if err != nil {
		return nil, err
	}

	id, _, err = er.ReadElementHeader()
	if err != nil {
		return nil, err
	}
	if id != matroska.IDSegment {
		return &Tracks{NotMatroska: true}, nil
	}
	segmentData := er.Position()

	tracksPos := int64(-1)
	jumped := false
	for step := 0; step < maxTopLevelSteps; step++ {
		start := er.Position()
		id, size, err := er.ReadElementHeader()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, ErrTracksNotFound
			}
			return nil, err
		}
		dataPos := er.Position()
		end := dataPos + int64(size)

		switch id {
		case matroska.IDTracks:
			if end-start > DefaultMaxBytes {
				return nil, ErrBudgetExceeded
			}
			raw, err := readAt(er, rs, start, end)
			if err != nil {
				return nil, err
			}
			return parseTracks(ebmlRaw, raw)
		case matroska.IDSeekHead:
			if size <= maxSeekHeadBytes {
				data, err := readAt(er, rs, dataPos, end)
				if err != nil {
					return nil, err
				}
				if rel, ok := seekHeadTracks(data); ok {
					tracksPos = segmentData + rel
				}
			}
		case matroska.IDCluster:
			if tracksPos < 0 || jumped {
				return nil, ErrTracksNotFound
			}
		}

		next := end
		if tracksPos >= 0 && !jumped {
			jumped = true
			next = tracksPos
		}
		if _, err := er.Seek(next, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return nil, ErrTracksNotFound
}

// readAt reads [from, to) of rs and leaves er (which wraps rs) at to.
func readAt(er *matroska.EBMLReader, rs io.ReadSeeker, from, to int64) ([]byte, error) {
	if to < from {
		return nil, fmt.Errorf("trackprobe: bad element bounds")
	}
	if _, err := rs.Seek(from, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, to-from)
	if _, err := io.ReadFull(rs, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, ErrTracksNotFound
		}
		return nil, err
	}
	if _, err := er.Seek(to, io.SeekStart); err != nil {
		return nil, err
	}
	return buf, nil
}

// seekHeadTracks returns the segment-relative position of Tracks listed in a
// SeekHead's data.
func seekHeadTracks(data []byte) (int64, bool) {
	r := matroska.NewEBMLReader(bytes.NewReader(data))
	for r.Position() < int64(len(data)) {
		el, err := r.ReadElement()
		if err != nil {
			return 0, false
		}
		if el.ID != matroska.IDSeek {
			continue
		}
		cr := matroska.NewEBMLReader(bytes.NewReader(el.Data))
		var seekID uint32
		var pos int64 = -1
		for cr.Position() < int64(len(el.Data)) {
			c, err := cr.ReadElement()
			if err != nil {
				break
			}
			switch c.ID {
			case matroska.IDSeekID:
				seekID = 0
				for _, b := range c.Data {
					seekID = seekID<<8 | uint32(b)
				}
			case matroska.IDSeekPos:
				pos = int64(c.ReadUInt())
			}
		}
		if seekID == matroska.IDTracks && pos >= 0 {
			return pos, true
		}
	}
	return 0, false
}

// parseTracks feeds the EBML header and the Tracks element to the matroska
// parser as a tiny file (EBML header, unknown-size Segment, Tracks).
func parseTracks(ebmlRaw, tracksRaw []byte) (*Tracks, error) {
	var syn bytes.Buffer
	syn.Grow(len(ebmlRaw) + 12 + len(tracksRaw))
	syn.Write(ebmlRaw)
	syn.Write([]byte{0x18, 0x53, 0x80, 0x67})                         // Segment
	syn.Write([]byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) // unknown size
	syn.Write(tracksRaw)

	p, err := matroska.NewMatroskaParser(bytes.NewReader(syn.Bytes()), false, matroska.IDTracks)
	if err != nil {
		return nil, fmt.Errorf("trackprobe: %w", err)
	}
	n := p.GetNumTracks()
	if n == 0 {
		return nil, ErrTracksNotFound
	}
	out := &Tracks{}
	for i := uint(0); i < n; i++ {
		ti := p.GetTrackInfo(i)
		if ti == nil {
			continue
		}
		t := Track{
			Languages: TrackLanguages(ti.Language, ti.LanguageIETF, ti.Name),
			Name:      strings.TrimSpace(ti.Name),
			Forced:    ti.Forced,
		}
		switch ti.Type {
		case matroska.TypeAudio:
			out.Audio = append(out.Audio, t)
		case matroska.TypeSubtitle:
			out.Subtitles = append(out.Subtitles, t)
		}
	}
	return out, nil
}

// LanguageSummary lists the distinct languages of tracks ("spa, eng"), for logs.
func LanguageSummary(tracks []Track) string {
	var keys []string
	seen := map[string]bool{}
	for _, t := range tracks {
		for _, l := range t.Languages {
			if !seen[l] {
				seen[l] = true
				keys = append(keys, l)
			}
		}
	}
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ", ")
}
