package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/haoyu010/ext.to/internal/media"
)

// aliasServer answers one title with an entry whose own name differs and whose
// alias equals the query, which is the shape a fansub release has.
//
// The query is compared with punctuation folded away, because TMDB's search
// ignores it: the release name carries "雪王来了！" while the alias is stored
// without the exclamation, and the request still finds the entry.
func aliasServer(t *testing.T, query, entryName, alias string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(media.Normalize(r.URL.Query().Get("query")), media.Normalize(query)) {
			write(w, map[string]any{"results": []any{}})
			return
		}
		write(w, map[string]any{"results": []map[string]any{
			{"id": 5150, "name": entryName, "first_air_date": "2023-08-25"},
		}})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/5150", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 5150, "name": entryName, "original_name": entryName,
			"first_air_date": "2023-08-25", "vote_average": 8.1, "vote_count": 12,
			"origin_country": []string{"CN"}, "original_language": "zh",
			"alternative_titles": map[string]any{
				"results": []map[string]any{{"iso_3166_1": "CN", "title": alias}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A fansub release publishes the name TMDB lists as an alias, not the entry's
// own name. Without accepting an alias the release never resolves, and since
// the classification rules need a match, most Chinese animation would never be
// forwarded.
func TestResolveMatchesAlias(t *testing.T) {
	srv := aliasServer(t, "雪王来了", "雪王驾到", "雪王来了！")
	c := newTestClient(t, srv)

	e, err := c.Resolve(context.Background(), "", "[Doomdos] - 雪王来了！ - 第14话 - [1080p BILIBILI COM WEB-DL]", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 5150 {
		t.Fatalf("ID = %d, want 5150", e.ID)
	}
	// An alias is weaker evidence than an exact name, so it is recorded as
	// such rather than claiming full confidence.
	if e.Confidence >= 0.9 {
		t.Errorf("Confidence = %v, want below an exact title match", e.Confidence)
	}
	if len(e.Aliases) == 0 {
		t.Error("Aliases were not carried on the entry")
	}
}

// An entry the search merely ranked highly must not be accepted, however
// plausible it looks. Otherwise every miss becomes a wrong match, and a wrong
// match decides the category.
func TestResolveRejectsUnrelatedHigherRankedEntry(t *testing.T) {
	srv := aliasServer(t, "雪王来了", "完全无关的作品", "另一个别名")
	c := newTestClient(t, srv)

	_, err := c.Resolve(context.Background(), "", "[Doomdos] - 雪王来了！ - 第14话", "")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
}

// A query too short to identify a work must not use the alias pass: a
// two-letter query matches something for any pair of letters. "Re" resolved an
// entry named "ARTE Re:" through its alias "Re:" before this guard existed.
func TestResolveAliasRejectsShortLatinQuery(t *testing.T) {
	srv := aliasServer(t, "Re", "ARTE Re:", "Re:")
	c := newTestClient(t, srv)

	_, err := c.Resolve(context.Background(), "", "Re: 從零開始的異世界的生活 - 84", "")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	// A short Chinese name is a complete title and must still be allowed.
	if !distinctiveQuery("黑门") {
		t.Error("a two-character Chinese name must stay eligible")
	}
}

// An exact name match must always beat an alias match, even when the aliased
// entry ranks higher or carries a closer year.
func TestResolveExactNameBeatsAlias(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []map[string]any{
			// Ranked first, matches only through an alias.
			{"id": 1, "name": "别名命中", "first_air_date": "2018-01-01"},
			// Ranked second, but its own name equals the query.
			{"id": 2, "name": "正确的作品", "first_air_date": "2018-01-01"},
		}})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/1", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 1, "name": "别名命中", "first_air_date": "2018-01-01",
			"alternative_titles": map[string]any{
				"results": []map[string]any{{"title": "正确的作品"}},
			},
		})
	})
	mux.HandleFunc("/tv/2", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 2, "name": "正确的作品", "first_air_date": "2018-01-01",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	e, err := c.Resolve(context.Background(), "", "正确的作品 S01E05", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 2 {
		t.Errorf("ID = %d, want the exact name match (2)", e.ID)
	}
}

// The alias pass must not multiply upstream requests. The search response is
// reused between the two passes, so a title costs one search per kind however
// many candidates it has, and only the alias pass adds detail requests, which
// are capped.
func TestResolveAliasPassDoesNotRepeatSearch(t *testing.T) {
	var mu sync.Mutex
	searches := map[string]int{}
	details := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		searches["tv"]++
		mu.Unlock()
		results := make([]map[string]any, 0, 9)
		for i := 0; i < 9; i++ {
			results = append(results, map[string]any{
				"id": 100 + i, "name": "排行第几都无关", "first_air_date": "2020-01-01",
			})
		}
		write(w, map[string]any{"results": results})
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []any{}})
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		details++
		mu.Unlock()
		write(w, map[string]any{"id": 100, "name": "排行第几都无关"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv)
	if _, err := c.Resolve(context.Background(), "", "某部没有对应条目的作品 S01E03", ""); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if searches["tv"] != 1 {
		t.Errorf("tv searches = %d, want exactly 1", searches["tv"])
	}
	if details > aliasCandidateLimit {
		t.Errorf("detail requests = %d, want at most %d", details, aliasCandidateLimit)
	}
}
