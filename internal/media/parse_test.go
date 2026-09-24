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
func TestParseFansubBracket(t *testing.T) {
	got := Parse("[kotopi] The World Is Dancing - 13 (WEB 1080p) (sub. español)")
	if got.Prefix != "" {
		t.Errorf("Prefix = %q, want empty", got.Prefix)
	}
	if got.Title != "The World Is Dancing - 13" {
		t.Errorf("Title = %q, want %q", got.Title, "The World Is Dancing - 13")
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
