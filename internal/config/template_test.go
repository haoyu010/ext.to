package config

import (
	"strings"
	"testing"
)

// A matched release must render the TMDB values.
func TestRenderTMDBFields(t *testing.T) {
	got := Render("{tmdb_title}|{tmdb_year}|{tmdb_rating}|{tmdb_votes}|{tmdb_url}|{tmdb_id}|{tmdb_type}",
		TemplateData{
			Title:      "Glass.Onion.2022.720p",
			TMDBTitle:  "利刃出鞘2",
			TMDBYear:   2022,
			TMDBRating: 7.14,
			TMDBVotes:  533052,
			TMDBURL:    "https://www.themoviedb.org/movie/661374",
			TMDBID:     661374,
			TMDBType:   "movie",
		})
	want := "利刃出鞘2|2022|7.1|533k|https://www.themoviedb.org/movie/661374|661374|movie"
	if got != want {
		t.Errorf("Render =\n  %q\nwant\n  %q", got, want)
	}
}

// A matched series must render the season the release stated, and it must come
// from the release name rather than from TMDB: a TMDB series covers every
// season in one entry, so the entry can only say which work the release is.
//
// The label carries its own leading space, so a template can put it straight
// after the title: a film renders without a gap or a stray separator, which is
// the case that a template written as "<b>{tmdb_title}</b> {season_label}"
// would get wrong.
func TestRenderSeasonLabel(t *testing.T) {
	cases := []struct {
		name string
		data TemplateData
		want string
	}{
		{
			name: "series with a stated season",
			data: TemplateData{TMDBTitle: "一人之下", TMDBYear: 2016, TMDBID: 67063, TMDBType: "tv", Season: 6},
			want: "<b>一人之下</b> 第 6 季 (2016)",
		},
		{
			name: "film states no season",
			data: TemplateData{TMDBTitle: "流浪地球2", TMDBYear: 2023, TMDBID: 842675, TMDBType: "movie"},
			want: "<b>流浪地球2</b> (2023)",
		},
		{
			name: "series with no stated season",
			data: TemplateData{TMDBTitle: "三体", TMDBYear: 2023, TMDBID: 204541, TMDBType: "tv"},
			want: "<b>三体</b> (2023)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Render(`<b>{tmdb_title}</b>{season_label} ({tmdb_year})`, tc.data)
			if got != tc.want {
				t.Errorf("Render =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
	// A season of zero is not a season: an unmatched release, or a complete
	// pack, must not render "第 0 季".
	if got := Render("{season}|{season_label}|{episode}", TemplateData{}); got != "||" {
		t.Errorf("empty numbers rendered as %q, want three empty fields", got)
	}
}

// An unmatched release must not render dangling values or stray separators.
func TestRenderUnmatchedFallsBackToTorrentFields(t *testing.T) {
	got := Render("{tmdb_title}|{tmdb_year}|{tmdb_rating}|{tmdb_votes}|{tmdb_url}",
		TemplateData{
			Title: "Lian Ross - V (Album) (2026)",
			URL:   "https://ext.to/lian-ross-22395846/",
		})
	want := "Lian Ross - V (Album) (2026)||||https://ext.to/lian-ross-22395846/"
	if got != want {
		t.Errorf("Render =\n  %q\nwant\n  %q", got, want)
	}
}

// A title containing markup must be escaped so Telegram accepts the caption.
func TestRenderEscapesTMDBTitle(t *testing.T) {
	got := Render("{tmdb_title}", TemplateData{TMDBTitle: "Tom & Jerry <2021>"})
	if !strings.Contains(got, "Tom &amp; Jerry &lt;2021&gt;") {
		t.Errorf("tmdb title was not escaped: %q", got)
	}
}

// The overview is truncated by rune count so multi-byte text stays intact.
func TestRenderTruncatesOverviewByRunes(t *testing.T) {
	long := strings.Repeat("字", 400)
	got := Render("{tmdb_overview}", TemplateData{TMDBOverview: long})
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected an ellipsis on a truncated overview, got %q", got[len(got)-10:])
	}
	if n := len([]rune(got)); n > 321 {
		t.Errorf("overview truncated to %d runes, want at most 321", n)
	}
	// Every rune must still be a valid CJK character plus the ellipsis.
	for _, r := range got {
		if r != '字' && r != '…' {
			t.Fatalf("multi-byte text was corrupted at %q", r)
		}
	}
}

func TestRenderDefaultTemplateWhenEmpty(t *testing.T) {
	got := Render("   ", TemplateData{Title: "Some Title"})
	if !strings.Contains(got, "Some Title") {
		t.Errorf("empty template should fall back to the default: %q", got)
	}
}

// humanCount is used for vote counts, where a compact form reads better.
func TestHumanCount(t *testing.T) {
	cases := map[int]string{0: "", -5: "", 42: "42", 999: "999", 1000: "1k", 533052: "533k", 2500000: "2.5m"}
	for in, want := range cases {
		if got := humanCount(in); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", in, got, want)
		}
	}
}

// The shipped TMDB template must only use placeholders the renderer knows.
func TestTMDBTemplatePlaceholdersAreAllSupported(t *testing.T) {
	known := map[string]bool{}
	for _, f := range TemplateFields {
		known[f.Key] = true
	}
	for _, key := range []string{
		"{tmdb_title}", "{tmdb_year}", "{tmdb_rating}", "{tmdb_votes}",
		"{tmdb_url}", "{category}", "{size}", "{files}", "{seeds}",
		"{leeches}", "{age}", "{url}",
	} {
		if !known[key] {
			t.Errorf("TMDBTemplate uses %s but TemplateFields does not list it", key)
		}
	}
	// Rendering it with a match must leave no placeholder behind.
	out := Render(TMDBTemplate, TemplateData{
		Title: "t", URL: "u", TMDBTitle: "匹配", TMDBYear: 2022,
		TMDBRating: 7.1, TMDBVotes: 1000, TMDBURL: "tu", TMDBID: 5, TMDBType: "movie",
	})
	if strings.Contains(out, "{") && strings.Contains(out, "}") {
		for _, f := range TemplateFields {
			if strings.Contains(out, f.Key) {
				t.Errorf("TMDBTemplate left %s unrendered", f.Key)
			}
		}
	}
}
