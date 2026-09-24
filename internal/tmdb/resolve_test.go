package tmdb

import (
	"context"
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
	// The canonical title must be attempted first, then the release title.
	if len(*queries) != 2 {
		t.Fatalf("queries = %v, want two attempts", *queries)
	}
	if !strings.Contains((*queries)[0], "透明") {
		t.Errorf("first query = %q, want the canonical title first", (*queries)[0])
	}
	if !strings.Contains((*queries)[1], "Love Unseen") {
		t.Errorf("second query = %q, want the release title", (*queries)[1])
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
	got := candidateTitles("The Long Watch", "The.Long.Watch.2026")
	if len(got) != 2 {
		t.Fatalf("got %v, want two candidates", got)
	}
	if got[0] != "The Long Watch" {
		t.Errorf("first candidate = %q, want the canonical title", got[0])
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
