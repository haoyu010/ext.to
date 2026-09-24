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

// A Chinese episode marker: 第14话, 第 14 話, 第01-12集. Fansub releases of
// Chinese animation mark episodes this way instead of with S01E02, and without
// this the title keeps the marker and matches nothing.
var reChineseEpisode = regexp.MustCompile(`第\s*\d{1,4}(?:\s*[-~]\s*\d{1,4})?\s*[话話集回]`)

// 1x02 style markers.
var reNxN = regexp.MustCompile(`(?:^|\s)(\d{1,2})x(\d{2})(?:\s|$)`)

// A detached episode number, as fansub anime releases write it:
// "[ANi] CANDY CARIES 蛀在糖糖裡 - 24 [1080P]". The number has to stand alone
// between separators so that a year or a resolution in the same position is
// not mistaken for an episode, and so that "Lian Ross - V (Album)" keeps its
// title. Only the tail is searched, because that is where the episode sits.
var reTrailingEpisode = regexp.MustCompile(`(?:^|\s)[-–—]\s*(\d{1,3})(?:\s|$|\[|【)`)

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

	// A name that begins with a separator is the tail of one that was cut
	// earlier, not a name.
	//
	// "[BDMV][251008-260325][桃源暗鬼 / Tougen Anki][BDMV][Vol.1-6 FIN][JPN]-YE"
	// loses every leading group to stripLeadingGroups and leaves "-YE", whose
	// cleaned form is "YE". Searching that resolves the release to a film
	// actually called "Ye!", and the wrong match then decides the category and
	// therefore whether the release is forwarded. Reporting no title is the
	// honest answer, and callers already treat an empty title as "unknown".
	if isDanglingTail(s) {
		return Result{}
	}

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
		// The pattern has two alternatives, so exactly one of the two capture
		// groups is populated and the other reports -1. Reading the wrong one
		// slices at -1 and panics, which is why the index is checked rather
		// than assumed: "Season 2" fills the second group and leaves the first
		// unset, the opposite of "S02".
		if loc[2] >= 0 {
			res.Season, _ = strconv.Atoi(s[loc[2]:loc[3]])
		} else if loc[4] >= 0 {
			res.Season, _ = strconv.Atoi(s[loc[4]:loc[5]])
		}
		res.Kind = KindTV
	} else if loc := reChineseEpisode.FindStringSubmatchIndex(s); loc != nil {
		// A fansub episode marker means a series, even with no season number.
		cut = min(cut, loc[0])
		res.Kind = KindTV
	} else if m := reTrailingEpisode.FindStringSubmatchIndex(s); m != nil {
		// A fansub anime release marks its episode as a detached number
		// ("[ANi] CANDY CARIES 蛀在糖糖裡 - 24") rather than with S01E24. Left
		// unrecognised, the release is searched as a film and can bind to a
		// same-named film entry, which then decides the category from the
		// wrong genre set and drops a Chinese release that should be kept.
		cut = min(cut, m[0])
		res.Episode, _ = strconv.Atoi(s[m[2]:m[3]])
		res.Kind = KindTV
	}

	// A Chinese season marker states the season outright ("一人之下 第六季"), so
	// the number is recorded rather than left to the caption: the TMDB match
	// identifies the series, which covers every season, and the season exists
	// only in the release name.
	//
	// It is read here rather than in the chain above because that chain stops at
	// the first marker it finds, and a name carries the season and the episode
	// in a fixed order: "时光代理人 第三季 [01-12]" has both, and cutting at the
	// season would leave the episode range in the title.
	if res.Season == 0 {
		if n, ok := chineseSeasonNumber(s); ok {
			res.Season = n
			res.Kind = KindTV
		}
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

// reDanglingTail matches a name that starts with a separator, which means the
// real title was cut off earlier and only the tail survived.
var reDanglingTail = regexp.MustCompile(`^\s*[-–—_.·:;/｜|]\s*`)

// isDanglingTail reports whether s is the leftover tail of a release name
// whose title was cut away, such as "-YE" or "-SHRI".
//
// The distinction matters because a short all-letters fragment looks exactly
// like a real title: "-YE" becomes "YE", and TMDB has a film named "Ye!". The
// leading separator is the only evidence that the text is a remnant, so it is
// what the test keys on.
//
// The whole string has to be dangling, not merely contain a separator: real
// titles use dashes internally ("Lian Ross - V (Album)") and must survive.
func isDanglingTail(s string) bool {
	m := reDanglingTail.FindString(s)
	if m == "" {
		return false
	}
	rest := strings.TrimSpace(s[len(m):])
	// A tail that still holds a meaningful phrase (more than one word) is
	// treated as a title, since cutting at the first separator would throw
	// away names such as "- A Quiet Place".
	return rest != "" && !strings.ContainsAny(rest, " /｜|")
}

// reAltSeparator splits the several names a fansub release lists for one work.
// Chinese animation is published as "中文名 / Romaji / English", so the full
// string matches nothing and each alternative has to be tried on its own.
var reAltSeparator = regexp.MustCompile(`\s*[/｜|]\s*`)

// reSegments splits a name on the separators fansub groups use between the
// group tag, the title and the episode.
var reSegments = regexp.MustCompile(`\s+[-–—]\s+`)

// SearchTitles returns the titles to try against TMDB for a release name, most
// likely first.
//
// Parse reports one title, which is right for a caption but not for a search:
// Chinese animation is published with a group tag, several alternative names
// and a 第14话 episode marker, and the cleaned title of such a name is often
// not what TMDB stores. Trying the alternatives costs a request each and only
// when the previous ones missed, so a release that already resolves is
// unaffected.
func SearchTitles(raw string) []string {
	var out []string
	seen := map[string]bool{}
	// Declared before it is assigned so the body can call itself: a candidate
	// yields further candidates (a sequel marker removed, a script run
	// isolated), and each is shorter than its parent, so this terminates.
	var add func(string)
	add = func(s string) {
		// Brackets are deliberately not in the trim set: trimming them here
		// unbalances a name such as "名称 [01-12][1080p]" and the group can no
		// longer be recognised, which leaves a term that matches nothing.
		s = strings.TrimSpace(strings.Trim(s, "-–—_.·:;,"))
		if s == "" {
			return
		}
		// A bracketed group is metadata, not part of a name, and a search term
		// that still carries one matches nothing. Groups are removed wherever
		// they sit, so a prefix, a suffix or both are handled the same way:
		// without this, "[喵萌奶茶屋] 时光代理人 第三季 [01-12][1080p]" keeps the
		// trailing episode range and resolution and resolves to nothing.
		//
		// The removal is not always a title. A name that is nothing but groups
		// leaves no text at all, and a group stack leaves only the release's
		// own tail ("[BDMV][…][桃源暗鬼 / Tougen Anki][JPN]-YE" -> "-YE"),
		// which must never be searched: it once resolved a release to an
		// unrelated film whose name happened to be the tail.
		if strings.ContainsAny(s, "[]【】") {
			stripped := stripBracketGroups(s)
			// A stray bracket with no partner cannot be cleaned, and a term
			// containing it matches nothing, so it is not offered.
			if stripped == s || stripped == "" || isDanglingTail(stripped) ||
				(!hasHan(stripped) && len([]rune(stripped)) < minLatinFragment) {
				return
			}
			add(stripped)
			return
		}
		if releaseTag[strings.ToLower(s)] {
			return
		}
		// A fragment that is only a number is a leftover episode or season
		// marker. Searching for it is not merely useless but harmful: TMDB has
		// works literally named "12" and "86", so "Kimi ga Shinu made Koi wo
		// Shitai - 12" would resolve to a Russian series called 12.
		if isNumericFragment(s) {
			return
		}
		if len([]rune(s)) < 2 || seen[s] {
			return
		}

		// A season marker is not part of the name TMDB stores. TMDB files a
		// series as one entry covering every season, so it has no entry called
		// "一人之下 第六季" and searching that name comes back empty; the base
		// name is the only one that can match. It is therefore searched in
		// place of the name as published, not after it, so a 国漫 release does
		// not spend a request on a query that cannot succeed.
		//
		// The name as published is still offered when the marked name could
		// itself be an entry: "毛骗 第二季" and "神探联盟第二季" are entries
		// whose own names carry the season, and the base search returns them
		// too, but only the caller comparing the marker can tell them from the
		// first season beside them.
		if base, marker := ChineseSeasonMarker(s); base != "" && marker != "" {
			add(base)
			return
		}

		seen[s] = true
		out = append(out, s)

		// A sequel marker is not part of the name TMDB stores: the release is
		// "幼女战记II" while the entry is "幼女战记", and "碧蓝之海3 Grand Blue
		// Dreaming!" while the entry is "碧蓝之海". Offering the name without
		// the marker is what lets those match at all. The original is kept, so
		// this only ever adds an attempt.
		if stripped := stripSequelMarker(s); stripped != "" {
			add(stripped)
		}

		// A dub note ("罗拉航海日记 中文配音") describes the release rather than
		// the work, so the name without it is offered as well.
		if stripped := stripDubbingMarker(s); stripped != "" {
			add(stripped)
		}

		// Fansub releases write the Chinese and Latin names as one candidate
		// ("碧蓝之海3 Grand Blue Dreaming!"), which matches nothing as a
		// whole. Each script run is offered on its own so the Chinese name can
		// reach the localised entry and the Latin one its alias.
		if len(splitByScript(s)) > 1 {
			for _, part := range splitByScript(s) {
				add(part)
			}
		}
	}

	for _, basis := range searchBases(raw) {
		add(basis)
		// The alternatives live in the text these forms produced, so splitting
		// them keeps whatever markers parsing already removed.
		for _, part := range reAltSeparator.Split(basis, -1) {
			add(part)
		}
		// A group tag can sit after a type prefix ("[剧集] [喵萌奶茶屋] 名称"), so
		// the segments between dashes are tried too.
		for _, seg := range reSegments.Split(basis, -1) {
			add(seg)
			for _, part := range reAltSeparator.Split(seg, -1) {
				add(part)
			}
		}
	}
	return out
}

// searchBases returns the strings a search term can be derived from, most
// likely first.
//
// Parse reports one title, which is right for a caption but not for a search.
// Three shapes need different treatment:
//
//   - A clean title ("[Shridhuu][1080p] GuAn / 一斩苍穹") is used as is.
//   - A name that is a stack of bracket groups
//     ("[BDMV][251008-260325][桃源暗鬼 / Tougen Anki][BDMV][Vol.1-6 FIN][JPN]")
//     has no clean title at all: the title sits inside one of the groups, so
//     the groups themselves are offered.
//   - A name whose every leading group was stripped may leave a remnant
//     ("-YE"), which is not a title and must never be searched.
func searchBases(raw string) []string {
	var out []string
	seen := map[string]bool{}
	push := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}

	primary := Parse(raw).Title
	push(primary)

	// The primary title keeps a bracket only when parsing could not find a
	// structural marker, which is exactly the stacked-group shape. The groups
	// are then the only place the real title appears.
	if strings.ContainsAny(primary, "[]【】") {
		for _, g := range titleBracketGroups(raw) {
			push(g)
			for _, part := range reAltSeparator.Split(g, -1) {
				push(part)
			}
		}
	}

	// The form with every leading group removed covers "[group][tag] 标题",
	// where Parse strips only the first group. A remnant of that stripping is
	// skipped: it is the tail of a name whose title was cut away.
	stripped := stripLeadingGroups(raw)
	if !isDanglingTail(stripped) {
		push(Parse(stripped).Title)
	}
	return out
}

// reBracketGroup captures one bracketed group and its contents.
var reBracketGroup = regexp.MustCompile(`[\[【]([^\]】]{2,120})[\]】]`)

// titleBracketGroups returns the contents of the bracketed groups in s, which
// is where the title lives when a release name is a stack of groups.
//
// Group contents that are clearly metadata rather than a name are dropped:
// release tags ("1080p"), resolution-like tokens and purely numeric groups
// ("251008-260325") are never titles. A single Latin word is also skipped,
// since it is far more often a group name ("BDMV") than a work; a Chinese
// word is kept, because for this corpus it usually is the work.
func titleBracketGroups(s string) []string {
	var out []string
	for _, m := range reBracketGroup.FindAllStringSubmatch(s, -1) {
		content := strings.TrimSpace(m[1])
		if content == "" || isNumericFragment(content) {
			continue
		}
		if releaseTag[strings.ToLower(content)] {
			continue
		}
		if !strings.ContainsAny(content, " /｜|") && !hasHan(content) &&
			len([]rune(content)) < 6 {
			continue
		}
		out = append(out, content)
	}
	return out
}

// hasHan reports whether s contains a CJK ideograph.
func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// stripSequelMarker removes a trailing sequel marker. It returns "" when
// nothing was removed or when the remainder is too short to be a title.
//
// A Chinese season marker is not handled here, because it is not merely
// removed: the name without it has to replace the name as published rather
// than be added after it, and the marker itself is still needed to choose
// between instalments. SearchTitles does both before calling this, so a name
// reaching here carries no season marker.
func stripSequelMarker(s string) string {
	// The glued alternative captures the title without its final Han
	// character, because the marker is what immediately follows it. Trying the
	// detached form first keeps the two cases from interfering.
	rest := ""
	if m := reSequelDetached.FindStringSubmatch(s); m != nil {
		rest = m[1]
	} else if m := reSequelGlued.FindStringSubmatch(s); m != nil {
		rest = m[1] + m[2]
	}
	rest = strings.TrimSpace(strings.Trim(rest, " -–—_.·:;,"))
	if len([]rune(rest)) < 2 || isNumericFragment(rest) {
		return ""
	}
	return rest
}

// reSequelDetached matches a marker that stands apart from the title.
//
// Roman numerals are case-sensitive on purpose. Releases write sequels as
// "II" or "IV", and matching them case-insensitively would also strip the
// "ix" off "The Mix" or the "vi" off "Vivi", offering searches for nonsense
// titles. The spelled-out forms are case-insensitive because "Part 2" and
// "part 2" both occur.
var reSequelDetached = regexp.MustCompile(`(?s)^(.+?)\s+(?:(?i:part\s*\d+|season\s*\d+|chapter\s*\d+)|\d{1,2}|[IVX]{1,5})$`)

// reSequelGlued matches a marker written straight onto a Han character, so the
// title keeps that character (group 2) and loses only the marker.
var reSequelGlued = regexp.MustCompile(`(?s)^(.+?)(\p{Han})(?:\d{1,2}|[IVX]{1,5})$`)

// reSequelSeason matches the Chinese season marker a release appends to a
// name, detached ("一人之下 第六季") or glued ("一人之下第六季").
//
// TMDB files the work without the marker: the entry is "一人之下" and there is
// no entry called "一人之下 第六季", so searching the name as published finds
// nothing. This is the shape mainland animation is published in, which makes
// it the difference between a 国漫 release resolving and being dropped.
//
// The marker is not covered by either pattern above: reSequelDetached only
// accepts Arabic digits and Latin words after the separator, and reSequelGlued
// requires the marker to be digits or roman numerals. A season written as
// 第六季 matched neither, so the name was searched whole and never resolved.
var reSequelSeason = regexp.MustCompile(`(?s)^(.+?)\s*(第\s*[0-9０-９一二三四五六七八九十百]+\s*[季部期])$`)

// ChineseSeasonMarker splits a release name into the name of the work and the
// season marker it carries, for example "一人之下 第六季" into "一人之下" and
// "第六季". It returns an empty marker when the name carries none.
//
// Searching the marker is what fails, and it fails hard: TMDB files a series
// as one entry covering every season, so it has no entry called "一人之下
// 第六季" and the search comes back empty. The base name is what TMDB knows.
//
// The marker is returned rather than discarded for two reasons. It has to be
// put back into the forwarded caption, because the match now identifies the
// series and the season is only in the release name. And dropping it can pick
// the wrong instalment: "毛骗 第二季" is its own entry beside "毛骗", so a
// search that ignores the marker has nothing to tell the two apart.
//
// This is deliberately narrower than stripSequelMarker, which also removes a
// bare number or a roman numeral. Those cannot be treated the same way,
// because TMDB gives a film an entry per instalment: "流浪地球2" is a separate
// entry from "流浪地球", and searching the stripped "流浪地球" would resolve a
// sequel to the first film. "第 N 季" is unambiguous in a way a bare "2" is
// not, so only that form is split here.
//
// Trailing bracketed groups are removed first, because they sit after the
// marker ("时光代理人 第三季 [01-12][1080p]") and would otherwise hide it.
func ChineseSeasonMarker(s string) (base, marker string) {
	s = strings.TrimSpace(stripBracketGroups(s))
	m := reSequelSeason.FindStringSubmatch(s)
	if m == nil {
		return "", ""
	}
	base = strings.TrimSpace(strings.Trim(m[1], " -–—_.·:;,"))
	if len([]rune(base)) < 2 || isNumericFragment(base) {
		return "", ""
	}
	return base, strings.TrimSpace(m[2])
}

// StatesSeason reports whether a name already writes out the given season, so
// that a caller appending the season does not repeat what the name says.
//
// TMDB files some seasons as their own entry whose name carries the season
// ("毛骗 第二季" beside "毛骗"), and the release that named that season is
// matched to it. The entry's own name is then the complete identification, and
// a caption built as name plus season would say it twice.
func StatesSeason(name string, season int) bool {
	if season <= 0 {
		return false
	}
	n, ok := chineseSeasonNumber(name)
	return ok && n == season
}

// reChineseSeasonNumber captures just the digits of a Chinese season marker,
// anywhere in the name rather than only at the end.
var reChineseSeasonNumber = regexp.MustCompile(`第\s*([0-9０-９]{1,3}|[一二三四五六七八九十百]+)\s*[季部期]`)

// chineseSeasonNumber reads the season a Chinese marker states, reporting
// false when the name carries none.
//
// It scans the whole name rather than its tail because the marker is followed
// by other fields: "时光代理人 第三季 [01-12]" states its season in the middle.
func chineseSeasonNumber(s string) (int, bool) {
	m := reChineseSeasonNumber.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	if n, err := strconv.Atoi(m[1]); err == nil {
		if n <= 0 {
			return 0, false
		}
		return n, true
	}
	n, ok := chineseNumber(m[1])
	if !ok || n <= 0 {
		return 0, false
	}
	return n, true
}

// chineseDigit maps the numerals that appear in a season marker to their value.
var chineseDigit = map[rune]int{
	'一': 1, '二': 2, '三': 3, '四': 4, '五': 5,
	'六': 6, '七': 7, '八': 8, '九': 9,
}

// chineseNumber parses the numerals a season marker uses, up to the hundreds.
// Seasons beyond that do not occur, and guessing at forms such as 零 would add
// cases nothing exercises.
func chineseNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	total, current := 0, 0
	for _, r := range s {
		switch {
		case r == '十':
			// "十" alone is ten; "二十" is twenty; "十八" is eighteen.
			if current == 0 {
				current = 1
			}
			total += current * 10
			current = 0
		case r == '百':
			if current == 0 {
				current = 1
			}
			total += current * 100
			current = 0
		default:
			d, ok := chineseDigit[r]
			if !ok {
				return 0, false
			}
			current = d
		}
	}
	return total + current, true
}

// reDubbingMarker matches a trailing dub or subtitle note.
//
// "罗拉航海日记 中文配音" is the same work as "罗拉航海日记", and TMDB stores
// only the latter. The note is a property of the release, not of the work, so
// it is offered in a stripped form too.
var reDubbingMarker = regexp.MustCompile(`(?s)\s*(?:中文配音|国语|粤语|台配|日语|双语|中字|简中|繁中|简繁|中英|字幕|内封|外挂|无修|未删减)\s*$`)

// stripDubbingMarker removes a trailing dub or subtitle note.
func stripDubbingMarker(s string) string {
	rest := strings.TrimSpace(strings.Trim(reDubbingMarker.ReplaceAllString(s, ""), " -–—_.·:;,"))
	if len([]rune(rest)) < 2 || rest == s {
		return ""
	}
	return rest
}

// splitByScript splits a name into its script runs, dropping the runs that
// mark an episode or a resolution.
//
// Fansub releases write the Chinese and Latin names as one string
// ("碧蓝之海3 Grand Blue Dreaming!"), which as a whole matches nothing. The
// Chinese run reaches the localised entry and the Latin run its alias, so each
// is worth offering separately. A single-script name returns one part and is
// left alone, because splitting it would only add noise.
func splitByScript(s string) []string {
	var parts []string
	var cur []rune
	var curClass int
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.TrimSpace(string(cur)))
			cur = nil
		}
	}
	for _, r := range s {
		var class int
		switch {
		case unicode.Is(unicode.Han, r):
			class = 1
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			class = 2
		case unicode.IsLetter(r):
			class = 3
		case unicode.IsDigit(r) || unicode.IsSpace(r):
			// Digits and spaces belong to whichever run surrounds them, so a
			// sequel marker ("碧蓝之海3") or a multi-word Latin name ("Grand
			// Blue Dreaming") is not split in two.
			cur = append(cur, r)
			continue
		default:
			// A separator ends the run. Keeping it would produce candidates
			// such as "GuAn /", which normalises to the same string as "GuAn"
			// and therefore shadows it in the caller's de-duplication.
			flush()
			curClass = 0
			continue
		}
		if curClass != 0 && class != curClass {
			flush()
		}
		curClass = class
		cur = append(cur, r)
	}
	flush()

	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.Trim(p, " -–—_.·:;,!/｜|"))
		if len([]rune(p)) < 2 || isNumericFragment(p) || releaseTag[strings.ToLower(p)] {
			continue
		}
		// A short Latin run in a mixed-script name is a prefix or an
		// abbreviation, not a title: splitting
		// "Re: 從零開始的異世界的生活 ... Zero kara Hajimeru Isekai Seikatsu"
		// yields "Re", and TMDB answers a two-letter query with whatever entry
		// happens to match it exactly ("ARTE Re:"). A short Chinese run is
		// left alone, because two characters are a complete title there
		// ("黑门").
		if !hasHan(p) && len([]rune(p)) < minLatinFragment {
			continue
		}
		out = append(out, p)
	}
	return out
}

// minLatinFragment is the shortest Latin run worth searching on its own.
const minLatinFragment = 3

// reLeadingGroup matches a bracketed group at the start of a release name.
//
// It allows a much longer name than rePrefix, which is capped at 8 characters
// because it only ever recognises a media type tag such as [剧集]. Fansub group
// names are far longer ("[喵萌奶茶屋&LoliHouse]"), and applying that cap here
// would leave the group in the title and match nothing.
var reLeadingGroup = regexp.MustCompile(`^\s*[\[【][^\]】]{1,60}[\]】]\s*`)

// stripLeadingGroups removes every bracketed group at the start of a release
// name. Fansub releases often carry two ("[Shridhuu][1080p] 名称") while Parse
// strips only the first, and the leftover bracket would corrupt the search
// term. Only leading groups are dropped: a bracket later in the name may be
// part of the title.
func stripLeadingGroups(s string) string {
	for {
		m := reLeadingGroup.FindString(s)
		if m == "" {
			return s
		}
		s = strings.TrimSpace(s[len(m):])
	}
}

// reAnyBracketGroup matches one bracketed group anywhere in a release name.
var reAnyBracketGroup = regexp.MustCompile(`[\[【][^\]】]{0,120}[\]】]`)

// stripBracketGroups removes every bracketed group from a release name, leaving
// the text between them.
//
// A bracketed suffix is as common as a bracketed prefix: "[喵萌奶茶屋] 时光代理人
// 第三季 [01-12][1080p]" carries the episode range and the resolution in
// groups at the end, and Parse keeps them because it cuts at a structural
// marker and there is none. Every candidate derived from the name then still
// contains a bracket, and a search term with a bracket matches nothing, so the
// release resolves to nothing at all.
//
// The caller has to check the result: for a name that is nothing but groups
// ("[BDMV][…][JPN]-YE") the removal leaves the release's own tail, which must
// not be searched.
func stripBracketGroups(s string) string {
	s = reAnyBracketGroup.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// isNumericFragment reports whether a fragment carries no letters at all, which
// means it is an episode number, a resolution or a year rather than a title.
func isNumericFragment(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
