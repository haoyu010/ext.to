package tmdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// titleServer records which queries were issued and answers only for the
// titles listed in answers.
func titleServer(t *testing.T, answers map[string]int) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		id, ok := answers[strings.ToLower(q)]
		if !ok {
			write(w, map[string]any{"results": []any{}})
			return
		}
		write(w, map[string]any{"results": []map[string]any{
			{"id": id, "name": q, "first_air_date": "2020-01-01"},
		}})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 777, "name": "Love Unseen", "first_air_date": "2020-01-01",
			"vote_average": 8.7, "vote_count": 2365,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &queries
}

// A traditional release name has to be searched in its simplified form as
// well. TMDB is queried with language=zh-CN and knows entries by their
// simplified names, so a traditional query can come back with nothing at all:
// measured against the live API, 進擊的巨人 最終季 returns no results while
// 进击的巨人 最终季 returns them.
//
// The folded spelling is offered as a second attempt inside the resolver
// rather than as an extra candidateTitles entry, because the candidate list
// dedupes on Normalize, which already folds: a folded variant added there
// would be discarded as a duplicate of the unfolded one and never searched.
func TestResolveFoldsTraditionalQuery(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		// The entry is indexed under its simplified name, so the traditional
		// spelling matches nothing: this is what makes the fold necessary
		// rather than cosmetic. Measured against the live API, the entry for
		// 漫長的季節 is listed as 漫长的季节.
		if q != "漫长的季节" {
			write(w, map[string]any{"results": []any{}})
			return
		}
		write(w, map[string]any{"results": []map[string]any{
			{"id": 225008, "name": "漫长的季节", "first_air_date": "2023-04-22"},
		}})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 225008, "name": "漫长的季节", "first_air_date": "2023-04-22",
			"vote_average": 8.9, "vote_count": 900,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)
	e, err := c.Resolve(context.Background(), "", "[剧集] 漫長的季節 (2023) S01E01", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 225008 {
		t.Errorf("ID = %d, want 225008", e.ID)
	}
	// The stated spelling is tried first, and the folded one is what resolves
	// it once that comes back empty.
	if len(queries) != 2 || !strings.ContainsAny(queries[0], "長節") {
		t.Fatalf("queries = %v, want the stated spelling first", queries)
	}
	if queries[1] != "漫长的季节" {
		t.Errorf("second query = %q, want the folded spelling", queries[1])
	}
}

// The fold must never replace the stated spelling, only supplement it. It is
// per character, so it also rewrites kanji shared with Japanese, and it
// narrows what TMDB returns rather than leaving it alone: measured against the
// live API, /search/movie for 戦場のヴァルキュリア3 誰がための銃瘡 returns the
// entry while the folded 戦场のヴァルキュリア3 誰がための銃瘡 returns nothing,
// so a fold-only query loses that film outright. 化物語 and 鬼滅の刃 無限列車編
// are the same on the movie endpoint.
func TestResolveKeepsTheStatedSpellingFirst(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	// The entry is indexed under the Japanese spelling only, and the folded
	// spelling is what a fold-only query would send.
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		if q != "戦場のヴァルキュリア3 誰がための銃瘡" {
			write(w, map[string]any{"results": []any{}})
			return
		}
		write(w, map[string]any{"results": []map[string]any{
			{"id": 1549734, "title": "戦場のヴァルキュリア3 誰がための銃瘡",
				"release_date": "2011-06-26"},
		}})
	})
	mux.HandleFunc("/movie/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 1549734, "title": "戦場のヴァルキュリア3 誰がための銃瘡",
			"original_title": "戦場のヴァルキュリア3 誰がための銃瘡",
			"release_date":   "2011-06-26", "vote_average": 7.2, "vote_count": 40,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)
	e, err := c.Resolve(context.Background(), "",
		"[电影] 戦場のヴァルキュリア3 誰がための銃瘡 (2011) 1080p", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 1549734 {
		t.Errorf("ID = %d, want 1549734", e.ID)
	}
	// The stated spelling has to be sent, and sending it has to be enough: the
	// folded spelling would return nothing for this title.
	if len(queries) == 0 || queries[0] != "戦場のヴァルキュリア3 誰がための銃瘡" {
		t.Fatalf("queries = %v, want the stated spelling first", queries)
	}
	if len(queries) != 1 {
		t.Errorf("queries = %v, want the stated spelling to settle it", queries)
	}
}

// The order of the search results is not a promise, and the entry a traditional
// release resolves to is often not the first one: measured against the live
// API, the query 进击的巨人 最终季 ranks two specials above 进击的巨人, and
// the match is made by that entry listing 进击的巨人 最终季 as an alias. The
// alias is stated in simplified, so the comparison only succeeds once both
// sides are folded.
func TestResolveMatchesTraditionalQueryAgainstSimplifiedAlias(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []map[string]any{
			{"id": 313028, "name": "进击的巨人 最终季 完结篇（后篇）", "first_air_date": "2023-11-05"},
			{"id": 1429, "name": "进击的巨人", "first_air_date": "2013-04-07"},
		}})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		id, name, aliases := 1429, "进击的巨人", []map[string]any{
			{"title": "进击的巨人 最终季"},
		}
		if strings.Contains(r.URL.Path, "313028") {
			id, name, aliases = 313028, "进击的巨人 最终季 完结篇（后篇）", nil
		}
		write(w, map[string]any{
			"id": id, "name": name, "first_air_date": "2013-04-07",
			"vote_average": 8.7, "vote_count": 4000,
			"alternative_titles": map[string]any{"results": aliases},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)
	e, err := c.Resolve(context.Background(), "", "[动漫] 進擊的巨人 最終季 - 01", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 1429 {
		t.Errorf("ID = %d, want 1429 (reached through the simplified alias)", e.ID)
	}
}

// The point of folding is that both sides end up on one script. A search that
// answers with the simplified name must satisfy a query that was written in
// the traditional one, or the release resolves to nothing even though the
// entry was found.
func TestResolveMatchesTraditionalNameAgainstSimplifiedEntry(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		// The entry is known to TMDB under the simplified name only.
		write(w, map[string]any{"results": []map[string]any{
			{"id": 225008, "name": "漫长的季节", "first_air_date": "2023-04-22"},
		}})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 225008, "name": "漫长的季节", "first_air_date": "2023-04-22",
			"vote_average": 8.9, "vote_count": 900,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)
	e, err := c.Resolve(context.Background(), "", "[剧集] 漫長的季節 (2023) S01E01", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 225008 {
		t.Errorf("ID = %d, want 225008", e.ID)
	}
}

// A series page reports the original (often non-Latin) name, which TMDB may
// not index. The English name derived from the release must still be tried.
func TestResolveFallsBackToReleaseTitle(t *testing.T) {
	srv, queries := titleServer(t, map[string]int{"love unseen beneath the clear night sky": 777})
	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)

	e, err := c.Resolve(context.Background(),
		"", "Love Unseen Beneath the Clear Night Sky S01E12 1080p WEB",
		"透明な夜に駆ける君と、目に見えない恋をした。")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 777 {
		t.Errorf("ID = %d, want 777", e.ID)
	}
	if e.MatchedBy != "title" {
		t.Errorf("MatchedBy = %q, want title", e.MatchedBy)
	}
	// The canonical title must be attempted first, then the release title. The
	// canonical spelling is Japanese, so folding rewrites it and the folded
	// form is offered as its own attempt in between; the stated spelling still
	// comes first, which is what keeps a Japanese name resolvable.
	if len(*queries) != 3 {
		t.Fatalf("queries = %v, want three attempts", *queries)
	}
	if !strings.Contains((*queries)[0], "透明") {
		t.Errorf("first query = %q, want the canonical title first", (*queries)[0])
	}
	if (*queries)[0] != "透明な夜に駆ける君と、目に見えない恋をした。" {
		t.Errorf("first query = %q, want the canonical title as stated", (*queries)[0])
	}
	last := (*queries)[len(*queries)-1]
	if !strings.Contains(last, "Love Unseen") {
		t.Errorf("last query = %q, want the release title", last)
	}
}

// When the canonical title already matches, no second request is made.
func TestResolveStopsAtCanonicalTitle(t *testing.T) {
	srv, queries := titleServer(t, map[string]int{"the long watch": 777})
	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)

	e, err := c.Resolve(context.Background(), "",
		"The.Long.Watch.S01E05-E07.2026.2160p.WEB-DL", "The Long Watch")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 777 {
		t.Errorf("ID = %d, want 777", e.ID)
	}
	if len(*queries) != 1 {
		t.Errorf("queries = %v, want exactly one", *queries)
	}
}

func TestCandidateTitles(t *testing.T) {
	got := candidateTitles("The Long Watch", "The.Long.Watch.2026.1080p")
	if len(got) != 1 {
		t.Fatalf("got %v, want the release title folded into the canonical one", got)
	}
	if got[0] != "The Long Watch" {
		t.Errorf("first candidate = %q, want the canonical title", got[0])
	}

	// A release title that adds nothing to the canonical one is not retried:
	// the search would repeat what the first attempt already did.
	if got := candidateTitles("The Long Watch", "The Long Watch"); len(got) != 1 {
		t.Errorf("got %v, want one candidate", got)
	}

	// A canonical title that only differs in punctuation must not be retried.
	// "The.Long.Watch" and "The Long Watch" normalise to the same form.
	if got := candidateTitles("The.Long.Watch", "The Long Watch"); len(got) != 1 {
		t.Errorf("got %v, want the duplicates folded away", got)
	}

	if got := candidateTitles("", ""); len(got) != 0 {
		t.Errorf("got %v, want no candidates", got)
	}
	if got := candidateTitles("  Only Canonical  ", ""); len(got) != 1 || got[0] != "Only Canonical" {
		t.Errorf("got %v, want the trimmed canonical title", got)
	}
}

// Chinese animation is published as "[字幕组] 中文名 / Romaji / English - 第14话".
// No single cleaned form of that matches TMDB, so the alternatives inside the
// release name have to be offered as separate candidates.
func TestCandidateTitlesIncludeFansubAlternatives(t *testing.T) {
	got := candidateTitles("", "[Shridhuu][1080p] GuAn / 一斩苍穹 / Yi Zhan Cangqiong - S01E10")
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
	}
	for _, want := range []string{"一斩苍穹", "GuAn", "Yi Zhan Cangqiong"} {
		if !found[want] {
			t.Errorf("candidates %v are missing %q", got, want)
		}
	}
	if len(got) < 2 {
		t.Fatalf("got %v, want the alternatives to be tried", got)
	}
}

// A name carrying a Chinese episode marker is a series, and the marker must not
// survive into the search title.
func TestCandidateTitlesDropChineseEpisodeMarker(t *testing.T) {
	got := candidateTitles("", "[Doomdos] - 罗拉航海日记 - 第24话 [1080p BILIBILI COM WEB-DL]")
	for _, g := range got {
		if strings.Contains(g, "第24话") || strings.Contains(g, "第 24 话") {
			t.Errorf("candidate %q still carries the episode marker", g)
		}
	}
}

// seasonServer answers a search with several instalments of one work, so which
// one the resolver picks is observable in the returned id.
//
// It models the two shapes TMDB uses: a series filed as one entry that covers
// every season, and a season filed as its own entry beside the base one.
func seasonServer(t *testing.T, results []map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("query"))
		mu.Unlock()
		write(w, map[string]any{"results": results})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		id := 0
		fmt.Sscanf(r.URL.Path, "/tv/%d", &id)
		var name string
		for _, rr := range results {
			if rr["id"] == id {
				name, _ = rr["name"].(string)
			}
		}
		write(w, map[string]any{
			"id": id, "name": name, "first_air_date": "2020-01-01",
			"vote_average": 8.0, "vote_count": 100,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &queries
}

// The query for a release that states a season must be the base name, never the
// name as published: TMDB files a series as one entry covering every season, so
// a search for "一人之下 第六季" matches nothing and the release would be lost.
func TestResolveSeasonSearchesBaseName(t *testing.T) {
	srv, queries := seasonServer(t, []map[string]any{
		{"id": 67063, "name": "一人之下", "first_air_date": "2016-07-09"},
	})
	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)

	e, err := c.Resolve(context.Background(), "", "[动漫] 一人之下 第六季 - 12", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 67063 {
		t.Errorf("ID = %d, want 67063", e.ID)
	}
	for _, q := range *queries {
		if strings.Contains(q, "季") {
			t.Errorf("query %q carries the season marker, which TMDB cannot match", q)
		}
	}
}

// When a season is filed as its own entry beside the base one, both come back
// for the base query and the marker has to choose. Without it the resolver
// picks whichever entry it happens to prefer, which is the first season.
func TestResolveSeasonPrefersTheStatedInstalment(t *testing.T) {
	srv, _ := seasonServer(t, []map[string]any{
		{"id": 78013, "name": "毛骗", "first_air_date": "2010-10-01"},
		{"id": 259602, "name": "毛骗 第二季 (2011)", "first_air_date": "2011-10-01"},
	})
	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)

	e, err := c.Resolve(context.Background(), "", "毛骗 第二季", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 259602 {
		t.Errorf("ID = %d, want 259602 (the second season, not the first)", e.ID)
	}
}

// A film sequel states no season, so nothing may be stripped from its name:
// "流浪地球2" is a separate entry from "流浪地球" and searching the base name
// would resolve the sequel to the first film.
func TestResolveFilmSequelKeepsItsNumber(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("query"))
		mu.Unlock()
		write(w, map[string]any{"results": []map[string]any{
			{"id": 842675, "title": "流浪地球2", "release_date": "2023-01-22"},
		}})
	})
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/movie/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 842675, "title": "流浪地球2", "release_date": "2023-01-22",
			"vote_average": 7.3, "vote_count": 761,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New("key", "zh-CN")
	c.SetBaseURL(srv.URL)
	e, err := c.Resolve(context.Background(), "", "[电影] 流浪地球2 (2023) 1080p", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 842675 {
		t.Errorf("ID = %d, want 842675", e.ID)
	}
	if len(queries) == 0 || !strings.Contains(queries[0], "2") {
		t.Errorf("queries = %v, want the sequel number kept", queries)
	}
}

// Year handling differs by media type because a torrent's year means
// different things for a film and for an episode of a series.
func TestYearCompatible(t *testing.T) {
	cases := []struct {
		name                   string
		kind                   string
		releaseYear, entryYear int
		want                   bool
	}{
		// A film premiering at a festival can be released the next year.
		{"movie same year", "movie", 2022, 2022, true},
		{"movie off by one", "movie", 2022, 2021, true},
		{"movie off by two", "movie", 2022, 2020, false},
		{"movie remake", "movie", 2022, 1982, false},
		// A series' year in a torrent name is the episode air year, which is
		// normally at or after the show's first air date.
		{"tv later episode", "tv", 2026, 2020, true},
		{"tv same year", "tv", 2026, 2026, true},
		{"tv announced early", "tv", 2026, 2027, true},
		{"tv clearly unrelated", "tv", 2026, 2035, false},
		// Missing data never blocks a match.
		{"no release year", "movie", 0, 1999, true},
		{"no entry year", "movie", 1999, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := yearCompatible(tc.kind, tc.releaseYear, tc.entryYear); got != tc.want {
				t.Errorf("yearCompatible(%q, %d, %d) = %v, want %v",
					tc.kind, tc.releaseYear, tc.entryYear, got, tc.want)
			}
		})
	}
}
