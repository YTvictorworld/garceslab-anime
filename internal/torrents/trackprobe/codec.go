package trackprobe

import "strings"

// Kind tells whether a subtitle track is text (can be read and machine
// translated as is) or images (would need OCR first).
type Kind string

const (
	KindText    Kind = "text"
	KindImage   Kind = "image"
	KindUnknown Kind = "unknown"
)

// codecInfo is what we know of a Matroska subtitle CodecID.
type codecInfo struct {
	label string // short name for logs and reports
	kind  Kind
}

// subtitleCodecs lists the Matroska subtitle CodecIDs
// (https://www.matroska.org/technical/codec_specs.html).
var subtitleCodecs = map[string]codecInfo{
	"S_TEXT/ASS":            {"ass", KindText},
	"S_ASS":                 {"ass", KindText}, // deprecated form
	"S_TEXT/SSA":            {"ssa", KindText},
	"S_SSA":                 {"ssa", KindText}, // deprecated form
	"S_TEXT/UTF8":           {"srt", KindText},
	"S_TEXT/ASCII":          {"srt", KindText},
	"S_TEXT/WEBVTT":         {"webvtt", KindText},
	"D_WEBVTT/SUBTITLES":    {"webvtt", KindText}, // older mkvmerge
	"D_WEBVTT/CAPTIONS":     {"webvtt", KindText},
	"D_WEBVTT/DESCRIPTIONS": {"webvtt", KindText},
	"S_TEXT/USF":            {"usf", KindText},
	"S_HDMV/TEXTST":         {"textst", KindText},
	"S_HDMV/PGS":            {"pgs", KindImage},
	"S_VOBSUB":              {"vobsub", KindImage},
	"S_VOBSUB/ZLIB":         {"vobsub", KindImage},
	"S_DVBSUB":              {"dvbsub", KindImage},
	"S_IMAGE/BMP":           {"bmp", KindImage},
}

// SubtitleKind derives the kind of a subtitle track from its CodecID. A
// missing or unlisted CodecID (S_KATE, S_ARIBSUB, …) is KindUnknown.
func SubtitleKind(codecID string) Kind {
	if ci, ok := subtitleCodecs[strings.ToUpper(strings.TrimSpace(codecID))]; ok {
		return ci.kind
	}
	return KindUnknown
}

// CodecLabel is a short name for a CodecID: "ass", "srt", "pgs", … for the
// known subtitle codecs, "aac", "opus", … for audio (A_AAC/MPEG4/LC → "aac"),
// the lower-cased CodecID otherwise and "unknown" when there is none.
func CodecLabel(codecID string) string {
	id := strings.ToUpper(strings.TrimSpace(codecID))
	if id == "" {
		return "unknown"
	}
	if ci, ok := subtitleCodecs[id]; ok {
		return ci.label
	}
	if rest, ok := strings.CutPrefix(id, "A_"); ok {
		if i := strings.IndexByte(rest, '/'); i > 0 {
			rest = rest[:i]
		}
		return strings.ToLower(rest)
	}
	return strings.ToLower(id)
}

// TrackLanguage is the language key of a track for logs and reports: its
// languages joined with "+" ("en+es" when the tag and the name disagree), or
// "und".
func TrackLanguage(t Track) string {
	if len(t.Languages) == 0 {
		return "und"
	}
	return strings.Join(t.Languages, "+")
}

// CodecSummary lists the tracks as language:codec in file order
// ("en:ass, es:ass, fr:pgs"), for logs. Forced tracks get a "(forced)" mark.
func CodecSummary(tracks []Track) string {
	if len(tracks) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(tracks))
	for _, t := range tracks {
		s := TrackLanguage(t) + ":" + CodecLabel(t.CodecID)
		if t.Forced {
			s += "(forced)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
