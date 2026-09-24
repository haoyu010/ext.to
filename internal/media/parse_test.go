package media

import (
	"strings"
	"testing"
)

// releaseNames are real titles taken from ext.to listing pages. They are the
// best regression corpus available because they include the punctuation and
// separator quirks that actually occur in the wild.
var releaseNames = []struct {
	in   string
	want Result
}{
	{in: "Ring.Ring.2019.1080p.WEBRip", want: Result{Title: "Ring Ring", Year: 2019, Kind: KindMovie}},
	{in: "Mrs. Doubtfire.1993.2160p.BDRemux.HDR.DV", want: Result{Title: "Mrs. Doubtfire", Year: 1993, Kind: KindMovie}},
	{in: "Glass Onion A Knives Out Mystery 2022 720p NF WEB-DL DDP5 1 Atmos H 264-Kitsune",
		want: Result{Title: "Glass Onion A Knives Out Mystery", Year: 2022, Kind: KindMovie}},
	{in: "Teen Titans Go S09E45 Teen Titans Go to the Repair Shop Pt 1 1080p AMZN WEB-DL DDP2 0 H 264-NTb",
		want: Result{Title: "Teen Titans Go", Season: 9, Episode: 45, Kind: KindTV}},
	{in: "Undercover Consequences (2021) 1080p WEBRip-LAMA",
		want: Result{Title: "Undercover Consequences", Year: 2021, Kind: KindMovie}},
	{in: "The.Long.Watch.S01E05-E07.2026.2160p.WEB-DL.H265.DV.DDP5.1-BlackTV",
		want: Result{Title: "The Long Watch", Year: 2026, Season: 1, Episode: 5, Kind: KindTV}},
	{in: "Babylon Berlin S02E04 720p AMZN WEB-DL DD 5 1 H 264-playWEB",
		want: Result{Title: "Babylon Berlin", Season: 2, Episode: 4, Kind: KindTV}},
	{in: "The.Predator.2018.1080p.DSNP.WEB-DL.DDP5.1.Atmos.DV.H.265-OnlyWeb",
		want: Result{Title: "The Predator", Year: 2018, Kind: KindMovie}},
	{in: "Rick.and.Morty.S03.1080p.HEVC.10bit.AAC.LATiNO.Eng.Sub.Spa-HeCviCu",
		want: Result{Title: "Rick and Morty", Season: 3, Kind: KindTV}},
	{in: "90 Day Fiance S12E20 Tell All Part 2 1080p AMZN WEB-DL DDP2 0 H 264-NTb",
		want: Result{Title: "90 Day Fiance", Season: 12, Episode: 20, Kind: KindTV}},
	{in: "The Floor.S08E16.PL.1080p.WEB-DL.H.264-XuploaD",
		want: Result{Title: "The Floor", Season: 8, Episode: 16, Kind: KindTV}},
	{in: "Sylv and the Ancient Tree", want: Result{Title: "Sylv and the Ancient Tree"}},
	{in: "Harry.Potter.And.The.Prisoner.Of.Azkaban.2004.1080p.DS4K.BDRip.HDR10.HEVC.AAC.mSubs-HeCviCu",
		want: Result{Title: "Harry Potter And The Prisoner Of Azkaban", Year: 2004, Kind: KindMovie}},
	{in: "Into the Blue 2005 1080p AMZN WEB-DL DDP 5 1 H 264-PiRaTeS",
		want: Result{Title: "Into the Blue", Year: 2005, Kind: KindMovie}},
	{in: "D O P E Unit S01E08 Finale 1080p AMZN WEB-DL DDP5 1 H 264-RAWR",
		want: Result{Title: "D O P E Unit", Season: 1, Episode: 8, Kind: KindTV}},
	{in: "Playboi Carti", want: Result{Title: "Playboi Carti"}},
	{in: "Lian Ross - V (Album) (Extended Versions) (2026)", want: Result{Title: "Lian Ross - V (Album) (Extended Versions)", Year: 2026}},
}

func TestParseReleaseNames(t *testing.T) {
	for _, tc := range releaseNames {
		t.Run(tc.in, func(t *testing.T) {
			got := Parse(tc.in)
			if got.Title != tc.want.Title {
				t.Errorf("Title = %q, want %q", got.Title, tc.want.Title)
			}
			if got.Year != tc.want.Year {
				t.Errorf("Year = %d, want %d", got.Year, tc.want.Year)
			}
			if tc.want.Kind != KindUnknown && got.Kind != tc.want.Kind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.want.Kind)
			}
			if got.Season != tc.want.Season {
				t.Errorf("Season = %d, want %d", got.Season, tc.want.Season)
			}
			if got.Episode != tc.want.Episode {
				t.Errorf("Episode = %d, want %d", got.Episode, tc.want.Episode)
			}
		})
	}
}

func TestParseChinesePrefix(t *testing.T) {
	got := Parse("[剧集] 漫长的季节 (2023) 1080p")
	if got.Kind != KindTV {
		t.Errorf("Kind = %q, want %q", got.Kind, KindTV)
	}
	if got.Prefix != "剧集" {
		t.Errorf("Prefix = %q, want 剧集", got.Prefix)
	}
	if got.Title != "漫长的季节" {
		t.Errorf("Title = %q, want 漫长的季节", got.Title)
	}
	if got.Year != 2023 {
		t.Errorf("Year = %d, want 2023", got.Year)
	}

	movie := Parse("[电影] 流浪地球 2 (2023)")
	if movie.Kind != KindMovie {
		t.Errorf("Kind = %q, want %q", movie.Kind, KindMovie)
	}
	if movie.Title != "流浪地球 2" {
		t.Errorf("Title = %q, want 流浪地球 2", movie.Title)
	}
}

// A bracketed fansub group is metadata, not a type hint, so it must be
// dropped without changing the inferred kind.
//
// The episode marker is stripped and the kind inferred as tv. The release is
// "[kotopi] The World Is Dancing - 13", and "The World Is Dancing - 13" is not
// a title TMDB stores, so keeping the marker meant the release could never
// match. The kind is a separate matter from the bracket: it comes from the
// detached episode number, not from the group name.
func TestParseFansubBracket(t *testing.T) {
	got := Parse("[kotopi] The World Is Dancing - 13 (WEB 1080p) (sub. español)")
	if got.Prefix != "" {
		t.Errorf("Prefix = %q, want empty", got.Prefix)
	}
	if got.Title != "The World Is Dancing" {
		t.Errorf("Title = %q, want %q", got.Title, "The World Is Dancing")
	}
	if got.Kind != KindTV {
		t.Errorf("Kind = %q, want tv", got.Kind)
	}
	if got.Episode != 13 {
		t.Errorf("Episode = %d, want 13", got.Episode)
	}
}

func TestNormalize(t *testing.T) {
	if got := Normalize("Mrs. Doubtfire"); got != "mrsdoubtfire" {
		t.Errorf("Normalize = %q, want mrsdoubtfire", got)
	}
	if Normalize("The Long Watch") != Normalize("the.long.watch") {
		t.Error("expected separators and case to fold together")
	}
}

func TestParseEmpty(t *testing.T) {
	if got := Parse("   "); got.Title != "" {
		t.Errorf("Title = %q, want empty", got.Title)
	}
}

// "Season 2" and "S02" are two alternatives in one pattern, so exactly one
// capture group is populated and the other reports -1. Reading the unset one
// slices at -1 and panics, which would take the whole forwarder down on a
// single release name.
func TestParseSeasonSpellingsDoNotPanic(t *testing.T) {
	cases := []struct {
		in     string
		season int
		title  string
	}{
		{"Show Season 2 1080p", 2, "Show"},
		{"Some Show Season 3 WEB-DL", 3, "Some Show"},
		{"Show S02 1080p", 2, "Show"},
	}
	for _, tc := range cases {
		got := Parse(tc.in)
		if got.Season != tc.season {
			t.Errorf("Parse(%q).Season = %d, want %d", tc.in, got.Season, tc.season)
		}
		if got.Title != tc.title {
			t.Errorf("Parse(%q).Title = %q, want %q", tc.in, got.Title, tc.title)
		}
		if got.Kind != KindTV {
			t.Errorf("Parse(%q).Kind = %q, want tv", tc.in, got.Kind)
		}
	}
}

// Chinese animation is published with 第14话 instead of S01E02. The marker has
// to be recognised as an episode and removed, or the title never matches.
func TestParseChineseEpisodeMarker(t *testing.T) {
	got := Parse("[Doomdos] - 罗拉航海日记 - 第24话 - [1080p BILIBILI COM WEB-DL]")
	if got.Kind != KindTV {
		t.Errorf("Kind = %q, want tv", got.Kind)
	}
	if strings.Contains(got.Title, "第24话") {
		t.Errorf("Title = %q, want the episode marker removed", got.Title)
	}

	// A range marker such as 第01-12集 marks a batch of episodes.
	if batch := Parse("碧蓝之海3 Grand Blue Dreaming! S3 第01-12集 GB_CN AV1_opus 1080p"); batch.Kind != KindTV {
		t.Errorf("Kind = %q, want tv for a batch release", batch.Kind)
	}
}

// SearchTitles feeds TMDB the alternatives a fansub release lists. A fragment
// that is only a number must never be offered: TMDB has works literally named
// "12", so searching for the episode number resolves a release to a completely
// unrelated series. A wrong match is worse than no match, because it decides
// the category and therefore whether the release is forwarded.
func TestSearchTitlesSkipEpisodeNumbers(t *testing.T) {
	for _, got := range SearchTitles("[喵萌奶茶屋&LoliHouse] 与你相恋到生命尽头 / Kimi ga Shinu made Koi wo Shitai - 12") {
		if isNumericFragment(got) {
			t.Errorf("SearchTitles offered the numeric fragment %q", got)
		}
	}
}

// The several names a release lists must each be offered, because no single
// cleaned form matches what TMDB stores.
func TestSearchTitlesOfferAlternatives(t *testing.T) {
	got := SearchTitles("[Shridhuu][1080p] GuAn / 一斩苍穹 / Yi Zhan Cangqiong - S01E10")
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
	}
	for _, want := range []string{"一斩苍穹", "GuAn", "Yi Zhan Cangqiong"} {
		if !found[want] {
			t.Errorf("SearchTitles %v is missing %q", got, want)
		}
	}
}

// Every candidate must be usable as a search term: a leftover bracket or a
// bare resolution tag makes the request match nothing.
func TestSearchTitlesAreClean(t *testing.T) {
	for _, in := range []string{
		"[Shridhuu][1080p] GuAn / 一斩苍穹 / Yi Zhan Cangqiong - S01E10",
		"[喵萌奶茶屋&LoliHouse] 与你相恋到生命尽头 / Kimi ga Shinu made Koi wo Shitai - 12 [WebRip 1080p HEVC-10bit AAC]",
		"[Doomdos] - 罗拉航海日记 - 第24话 - [1080p BILIBILI COM WEB-DL]",
	} {
		for _, got := range SearchTitles(in) {
			if strings.ContainsAny(got, "[]【】") {
				t.Errorf("candidate %q for %q still carries brackets", got, in)
			}
			if releaseTag[strings.ToLower(got)] {
				t.Errorf("candidate %q for %q is a release tag", got, in)
			}
			if isNumericFragment(got) {
				t.Errorf("candidate %q for %q is numeric", got, in)
			}
		}
	}
}

// A release name that is a stack of bracket groups has no title outside the
// groups, so the group contents are the only thing worth searching.
//
// Parsing it whole used to leave a remnant: every leading group was stripped
// and the tail "-YE" was then read as a title, resolving the release to a film
// actually called "Ye!". A wrong match is worse than no match, because it
// decides the category and therefore whether the release is forwarded.
func TestSearchTitlesReadStackedGroups(t *testing.T) {
	got := SearchTitles("[BDMV][251008-260325][桃源暗鬼 / Tougen Anki][BDMV][Vol.1-6 FIN][JPN]-YE")
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
		// The remnant of stripping must never be offered.
		if Normalize(g) == "ye" {
			t.Errorf("candidate %q is the stripped tail, not a title", g)
		}
		// The group's own name is metadata, not a work.
		if g == "BDMV" {
			t.Errorf("candidates %v include the group name", got)
		}
	}
	if !found["桃源暗鬼"] {
		t.Errorf("candidates %v are missing the title from the group", got)
	}
}

// A sequel marker is not part of the name TMDB stores, so the name without it
// has to be offered. The marker is only recognised as its own token: a looser
// rule would eat the last letter of an ordinary word, because "Youjo Senki"
// and "Shitai" both end in "i".
func TestSearchTitlesStripSequelMarker(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"幼女战记II", "幼女战记"},
		{"碧蓝之海3", "碧蓝之海"},
		{"Clevatess II", "Clevatess"},
		{"流浪地球 2", "流浪地球"},
	}
	for _, tc := range cases {
		if got := stripSequelMarker(tc.in); got != tc.want {
			t.Errorf("stripSequelMarker(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A title that merely ends in a letter must survive untouched.
	for _, in := range []string{"Youjo Senki", "Kimi ga Shinu made Koi wo Shitai", "碧蓝之海"} {
		if got := stripSequelMarker(in); got != "" {
			t.Errorf("stripSequelMarker(%q) = %q, want no change", in, got)
		}
	}
}

// A dub note describes the release rather than the work, so the name without
// it is offered as well.
func TestSearchTitlesStripDubbingMarker(t *testing.T) {
	if got := stripDubbingMarker("罗拉航海日记 中文配音"); got != "罗拉航海日记" {
		t.Errorf("stripDubbingMarker = %q, want 罗拉航海日记", got)
	}
	// A name with no note must not be altered.
	if got := stripDubbingMarker("罗拉航海日记"); got != "" {
		t.Errorf("stripDubbingMarker = %q, want no change", got)
	}
}

// Fansub releases write the Chinese and Latin names as one candidate, which
// matches nothing as a whole. Each script run has to be offered on its own.
func TestSearchTitlesSplitScripts(t *testing.T) {
	got := SearchTitles("【极影字幕·毁片党】碧蓝之海3 Grand Blue Dreaming! S3 第01-12集 GB_CN AV1_opus 1080p")
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
	}
	if !found["碧蓝之海"] {
		t.Errorf("candidates %v are missing the Chinese name without its sequel number", got)
	}
	if !found["Grand Blue Dreaming"] {
		t.Errorf("candidates %v are missing the Latin name", got)
	}
}

// Bracketed groups are metadata wherever they sit. A trailing group such as an
// episode range or a resolution cannot be left in a search term: TMDB matches
// nothing against "名称 [01-12][1080p]", so the release resolves to nothing at
// all even though its title was parsed correctly.
func TestSearchTitlesStripTrailingGroups(t *testing.T) {
	got := SearchTitles("[喵萌奶茶屋] 时光代理人 第三季 [01-12][1080p]")
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
	}
	if !found["时光代理人"] {
		t.Errorf("candidates %v are missing the title once the trailing groups go", got)
	}
	for _, g := range got {
		if strings.ContainsAny(g, "[]【】") {
			t.Errorf("candidate %q still carries a bracket", g)
		}
	}
}

// A season marker must never reach TMDB as part of the query. TMDB files a
// series as one entry covering every season, so it has no entry called
// "一人之下 第六季" and that search comes back empty: offering it first spends a
// request on a query that cannot succeed, and offering only it means the
// release never resolves at all.
//
// The name as published must not be offered either. This is the difference
// from a sequel marker, which is only ever an extra attempt; here the marked
// name is the one form that cannot match, so it is replaced rather than
// appended.
func TestSearchTitlesReplaceSeasonMarkerWithBaseName(t *testing.T) {
	cases := []struct {
		in    string
		base  string
		first string
	}{
		{"[动漫] 一人之下 第六季 - 12", "一人之下", "一人之下"},
		{"一人之下第六季", "一人之下", "一人之下"},
		{"吞噬星空 第2季", "吞噬星空", "吞噬星空"},
		{"斗罗大陆 第10部", "斗罗大陆", "斗罗大陆"},
	}
	for _, tc := range cases {
		got := SearchTitles(tc.in)
		if len(got) == 0 {
			t.Errorf("SearchTitles(%q) offered nothing", tc.in)
			continue
		}
		if got[0] != tc.first {
			t.Errorf("SearchTitles(%q)[0] = %q, want the base name %q", tc.in, got[0], tc.first)
		}
		for _, g := range got {
			if strings.Contains(g, "季") || strings.Contains(g, "部") {
				t.Errorf("SearchTitles(%q) offered %q, which carries the season marker", tc.in, g)
			}
		}
	}
}

// ChineseSeasonMarker has to report the base name and the marker separately,
// because the search uses one and the caption uses the other.
func TestChineseSeasonMarker(t *testing.T) {
	cases := []struct{ in, base, marker string }{
		{"一人之下 第六季", "一人之下", "第六季"},
		{"一人之下第六季", "一人之下", "第六季"},
		{"吞噬星空 第2季", "吞噬星空", "第2季"},
		{"斗罗大陆 第10部", "斗罗大陆", "第10部"},
		{"名称 - 第二季", "名称", "第二季"},
		{"毛骗 第二季", "毛骗", "第二季"},
		// Trailing metadata sits after the marker and must not hide it.
		{"时光代理人 第三季 [01-12][1080p]", "时光代理人", "第三季"},
		// A film sequel is not a season, and stripping it would resolve the
		// sequel to the first film, so it must not be reported as one.
		{"流浪地球2", "", ""},
		{"Clevatess II", "", ""},
		// No marker at all.
		{"一人之下", "", ""},
		{"流浪地球", "", ""},
	}
	for _, tc := range cases {
		base, marker := ChineseSeasonMarker(tc.in)
		if base != tc.base || marker != tc.marker {
			t.Errorf("ChineseSeasonMarker(%q) = (%q, %q), want (%q, %q)",
				tc.in, base, marker, tc.base, tc.marker)
		}
	}
}

// The season a release states is recorded, because the TMDB match identifies
// the series and the entry covers every season: only the release name says
// which instalment it is, and the caption has to show that.
func TestParseRecordsChineseSeason(t *testing.T) {
	cases := []struct {
		in     string
		season int
		kind   Kind
	}{
		{"[动漫] 一人之下 第六季 - 12", 6, KindTV},
		{"[喵萌奶茶屋] 时光代理人 第三季 [01-12][1080p]", 3, KindTV},
		{"斗罗大陆 第10部 - 01", 10, KindTV},
		{"某剧 第十八季", 18, KindTV},
		{"某剧 第二十三季", 23, KindTV},
		// A film sequel states no season.
		{"[电影] 流浪地球2 (2023)", 0, KindMovie},
		// A name with no marker states none.
		{"[剧集] 三体 (2023)", 0, KindTV},
	}
	for _, tc := range cases {
		got := Parse(tc.in)
		if got.Season != tc.season {
			t.Errorf("Parse(%q).Season = %d, want %d", tc.in, got.Season, tc.season)
		}
		if got.Kind != tc.kind {
			t.Errorf("Parse(%q).Kind = %q, want %q", tc.in, got.Kind, tc.kind)
		}
	}
}

// The season is only ever read from a marker that stands at the end, so a
// number carried elsewhere in a name is not mistaken for one.
func TestChineseSeasonRejectsNonMarkers(t *testing.T) {
	for _, in := range []string{
		"十八季的怪谈",
		"第十季风云",
		"我们的第一季回忆",
		"第10集",
		"第三期",
	} {
		base, marker := ChineseSeasonMarker(in)
		if base != "" || marker != "" {
			t.Errorf("ChineseSeasonMarker(%q) = (%q, %q), want no marker", in, base, marker)
		}
	}
}

// A season marker belongs to ChineseSeasonMarker alone, because removing it is
// not the whole job: the stripped name has to replace the name as published,
// and the marker has to survive for choosing between instalments. Gathering
// the two into one function silently made this branch unreachable, so the
// division is asserted here.
func TestStripSequelMarkerLeavesSeasonMarkers(t *testing.T) {
	for _, in := range []string{"一人之下 第六季", "一人之下第六季", "吞噬星空 第2季", "斗罗大陆 第10部"} {
		if got := stripSequelMarker(in); got != "" {
			t.Errorf("stripSequelMarker(%q) = %q, want no change: a season marker is not a sequel marker",
				in, got)
		}
	}
}

// Removing every group from a name that is nothing but groups leaves the
// release's own tail, which must never be searched: it is not a title, and a
// work that happens to share the tail's spelling would be matched wrongly.
func TestSearchTitlesDoNotSearchGroupStackTail(t *testing.T) {
	for _, g := range SearchTitles("[BDMV][251008-260325][桃源暗鬼 / Tougen Anki][BDMV][Vol.1-6 FIN][JPN]-YE") {
		if Normalize(g) == "ye" {
			t.Errorf("candidate %q is the stripped tail, not a title", g)
		}
	}
}

// A detached episode number marks a series. Unrecognised, the release is
// searched as a film and can bind to a same-named film entry, which then
// decides the category from the wrong genre set.
func TestParseDetachedEpisodeNumber(t *testing.T) {
	got := Parse("[ANi]  CANDY CARIES 蛀在糖糖裡 - 24 [1080P][Baha][WEB-DL][AAC AVC][CHT][MP4]")
	if got.Kind != KindTV {
		t.Errorf("Kind = %q, want tv", got.Kind)
	}
	if got.Episode != 24 {
		t.Errorf("Episode = %d, want 24", got.Episode)
	}
	if got.Title != "CANDY CARIES 蛀在糖糖裡" {
		t.Errorf("Title = %q, want the episode number removed", got.Title)
	}

	// A title with an internal dash must keep it.
	if lian := Parse("Lian Ross - V (Album) (Extended Versions) (2026)"); lian.Episode != 0 {
		t.Errorf("Episode = %d, want no episode for a dashed title", lian.Episode)
	}
}
