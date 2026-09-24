// Package media derives a canonical movie or series title from a torrent
// release name so it can be matched against TMDB.
//
// Only the release name itself is ever inspected. Descriptions, share text,
// subtitle notes, sizes and outbound links are deliberately ignored: they
// carry noise that makes fuzzy matching worse, not better.
package media

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Kind is the media type a release refers to.
type Kind string

const (
	KindUnknown Kind = ""
	KindMovie   Kind = "movie"
	KindTV      Kind = "tv"
)

// Result is the title information recovered from a release name.
type Result struct {
	// Title is the cleaned name to search TMDB with.
	Title string
	// Year is the release year, or 0 when the name carries none.
	Year int
	// Kind is movie or tv.
	Kind Kind
	// Season and Episode are zero when the name carries no episode marker.
	Season  int
	Episode int
	// Prefix records a bracketed type tag such as [剧集] when present, so
	// callers can tell an explicit hint apart from an inferred guess.
	Prefix string
}

// prefixTags maps bracketed leading tags to a media kind. Trackers that
// publish Chinese-tagged releases use these to state the type outright.
var prefixTags = []struct {
	tag  string
	kind Kind
}{
	{"剧集", KindTV},
	{"电视剧", KindTV},
	{"美剧", KindTV},
	{"英剧", KindTV},
	{"韩剧", KindTV},
	{"日剧", KindTV},
	{"动漫", KindTV},
	{"动画", KindTV},
	{"综艺", KindTV},
	{"电影", KindMovie},
	{"影片", KindMovie},
	{"纪录片", KindUnknown},
}

var rePrefix = regexp.MustCompile(`^\s*[\[【]\s*([^\]】]{1,8})\s*[\]】]\s*`)

// A trailing parenthesised year such as "(2021)".
var reParenYear = regexp.MustCompile(`[\(\[]((?:19|20)\d{2})[\)\]]`)

// Standalone four digit year.
var reYear = regexp.MustCompile(`(?:^|[\s])((?:19|20)\d{2})(?:[\s]|$)`)

// S01E02, S01E05-E07, s1e2.
var reSeasonEpisode = regexp.MustCompile(`(?i)(?:^|\s)S(\d{1,2})[\s._-]?E(\d{1,3})`)

// A season with no episode: S03, Season 3.
var reSeasonOnly = regexp.MustCompile(`(?i)(?:^|\s)(?:S(\d{1,2})|Season[\s._-]?(\d{1,2}))(?:[\s]|$)`)

// 1x02 style markers.
var reNxN = regexp.MustCompile(`(?:^|\s)(\d{1,2})x(\d{2})(?:\s|$)`)

// releaseTag matches tokens that only ever appear in the release-suffix part
// of a name, never as part of a real title. The list is intentionally
// conservative: a false positive truncates a title and ruins the match.
var releaseTag = map[string]bool{}

func init() {
	tokens := []string{
		// resolution
		"480p", "576p", "720p", "1080p", "1440p", "2160p", "4320p", "4k", "8k",
		"m720p", "m1080p", "hd", "sd", "uhd", "hdrip", "fullhd",
		// source
		"web", "webdl", "webrip", "web-dl", "webdlrip", "bluray", "blu-ray",
		"bdrip", "brrip", "bdremux", "remux", "dvdrip", "dvd", "hdtv", "hdcam",
		"cam", "ts", "tc", "r5", "screener", "hdts", "ds4k",
		// codec
		"x264", "x265", "h264", "h265", "h", "264", "265", "hevc", "avc", "av1",
		"xvid", "divx", "vp9", "10bit", "8bit", "12bit", "hi10p",
		// audio
		"aac", "aac2", "ac3", "eac3", "ddp", "dd", "ddp5", "ddp2", "dts",
		"dtshd", "truehd", "atmos", "flac", "mp3", "opus", "dtsx",
		// video dynamic range
		"hdr", "hdr10", "sdr", "hlg", "dv", "dovi", "hdr10plus",
		// misc release flags
		"proper", "repack", "internal", "extended", "unrated", "remastered",
		"multi", "multisub", "dualaudio", "dual", "subs", "sub", "msub",
		"msubs", "hardcoded", "hc",
		// streaming service tags
		"amzn", "nf", "dsnp", "atvp", "hmax", "hulu", "pcok", "itunes", "it",
		"stan", "cbs", "abc", "nbc", "bbc", "zee5", "hotstar",
		// language / region noise that trails a release name
		"eng", "english", "spa", "spanish", "esp", "ita", "italian", "fre",
		"french", "ger", "german", "rus", "russian", "lat", "latino", "pl",
		"polish", "cz", "cze", "kor", "jpn", "chi", "chs", "cht", "vostfr",
		"eztv", "pm",
	}
	for _, t := range tokens {
		releaseTag[t] = true
	}
}

// Parse recovers the title, year and type from a release name.
func Parse(raw string) Result {
	var res Result
	s := strings.TrimSpace(raw)
	if s == "" {
		return res
	}

	s = stripPrefix(s, &res)
	if s == "" {
		return res
	}

	s = NormalizeSeparators(s)

	// Locate the earliest structural marker: an episode marker or a year.
	// Everything before it is the title.
	cut := len(s)
	if loc := reSeasonEpisode.FindStringSubmatchIndex(s); loc != nil {
		cut = min(cut, loc[0])
		res.Season, _ = strconv.Atoi(s[loc[2]:loc[3]])
		res.Episode, _ = strconv.Atoi(s[loc[4]:loc[5]])
		res.Kind = KindTV
	} else if loc := reNxN.FindStringSubmatchIndex(s); loc != nil {
		cut = min(cut, loc[0])
		res.Season, _ = strconv.Atoi(s[loc[2]:loc[3]])
		res.Episode, _ = strconv.Atoi(s[loc[4]:loc[5]])
		res.Kind = KindTV
	} else if loc := reSeasonOnly.FindStringSubmatchIndex(s); loc != nil {
		cut = min(cut, loc[0])
		season := s[loc[2]:loc[3]]
		if loc[4] >= 0 {
			season = s[loc[4]:loc[5]]
		}
		res.Season, _ = strconv.Atoi(season)
		res.Kind = KindTV
	}

	if y, at := findYear(s); y != 0 {
		cut = min(cut, at)
		res.Year = y
	}

	// Without any structural marker, fall back to cutting at the release
	// suffix so at least the leading words survive.
	if cut == len(s) {
		if at := firstTagOffset(s); at != -1 {
			cut = at
		}
	}

	title := cleanTitle(s[:cut])
	if title == "" {
		// The marker sat at the very start; retry against the whole string
		// so a name like "2021 Something" still yields something usable.
		title = cleanTitle(stripTags(s))
	}
	res.Title = title

	// A bracketed type tag outranks inference from the shape of the name.
	if res.Prefix != "" {
		if k := kindForTag(res.Prefix); k != KindUnknown {
			res.Kind = k
		}
	}
	if res.Kind == KindUnknown && res.Year != 0 {
		res.Kind = KindMovie
	}
	return res
}

// stripPrefix removes a leading bracketed type tag and records it.
func stripPrefix(s string, res *Result) string {
	m := rePrefix.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	tag := strings.TrimSpace(m[1])
	for _, p := range prefixTags {
		if strings.EqualFold(tag, p.tag) {
			res.Prefix = tag
			return strings.TrimSpace(s[len(m[0]):])
		}
	}
	// An unrelated leading bracket such as a fansub group name: drop it, it
	// is metadata rather than part of the title.
	return strings.TrimSpace(s[len(m[0]):])
}

func kindForTag(tag string) Kind {
	for _, p := range prefixTags {
		if strings.EqualFold(tag, p.tag) {
			return p.kind
		}
	}
	return KindUnknown
}

// NormalizeSeparators converts dot and underscore separators into spaces
// while preserving genuine punctuation such as "Mrs." and "M.I.A.".
func NormalizeSeparators(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i := 0; i < len(r); i++ {
		switch {
		case r[i] == '_':
			b.WriteRune(' ')
		case r[i] == '.':
			if isInitialDot(r, i) {
				b.WriteRune('.')
			} else {
				b.WriteRune(' ')
			}
		default:
			b.WriteRune(r[i])
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// isInitialDot reports whether the dot at index i is punctuation rather than
// a field separator. It is punctuation when it terminates an abbreviation
// followed by a space ("Mrs. Doubtfire") or sits between single initials
// ("M.I.A.").
func isInitialDot(r []rune, i int) bool {
	if i+1 < len(r) && unicode.IsSpace(r[i+1]) {
		return true
	}
	if i == 0 || i+1 >= len(r) {
		return false
	}
	if !unicode.IsLetter(r[i-1]) || !unicode.IsLetter(r[i+1]) {
		return false
	}
	// The letter before must stand alone, meaning it is not preceded by
	// another letter.
	if i-2 >= 0 && unicode.IsLetter(r[i-2]) {
		return false
	}
	// The letter after must stand alone too.
	if i+2 < len(r) && unicode.IsLetter(r[i+2]) {
		return false
	}
	return true
}

// findYear returns the first plausible release year and its offset.
func findYear(s string) (int, int) {
	if m := reParenYear.FindStringSubmatchIndex(s); m != nil {
		y, _ := strconv.Atoi(s[m[2]:m[3]])
		return y, m[0]
	}
	if m := reYear.FindStringSubmatchIndex(s); m != nil {
		y, _ := strconv.Atoi(s[m[2]:m[3]])
		return y, m[0]
	}
	return 0, 0
}

// firstTagOffset returns the offset of the earliest release-suffix token, or
// -1 when the name contains none.
func firstTagOffset(s string) int {
	off := 0
	for _, tok := range strings.Fields(s) {
		word := strings.ToLower(strings.Trim(tok, "[](){}"))
		if releaseTag[word] {
			return off
		}
		off += len(tok) + 1
	}
	return -1
}

// stripTags removes every release-suffix token from s.
func stripTags(s string) string {
	var keep []string
	for _, tok := range strings.Fields(s) {
		word := strings.ToLower(strings.Trim(tok, "[](){}"))
		if !releaseTag[word] {
			keep = append(keep, tok)
		}
	}
	return strings.Join(keep, " ")
}

// cleanTitle collapses whitespace and trims separator punctuation from the
// ends of a title. Brackets are deliberately left alone: cutting at a year
// or an episode marker can leave a title ending in ")" or "]", and stripping
// those would corrupt names such as "Lian Ross - V (Album)".
func cleanTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " -–—_.·:;,'\"")
	return strings.TrimSpace(s)
}

// Normalize folds a title to a comparable form: case, punctuation and
// accents are removed so "Mrs. Doubtfire" and "mrs doubtfire" agree.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'à', 'á', 'â', 'ã', 'ä', 'å':
			r = 'a'
		case 'è', 'é', 'ê', 'ë':
			r = 'e'
		case 'ì', 'í', 'î', 'ï':
			r = 'i'
		case 'ò', 'ó', 'ô', 'õ', 'ö', 'ø':
			r = 'o'
		case 'ù', 'ú', 'û', 'ü':
			r = 'u'
		case 'ñ':
			r = 'n'
		case 'ç':
			r = 'c'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
