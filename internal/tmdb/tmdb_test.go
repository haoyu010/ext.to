package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixtureServer emulates the TMDB endpoints the client uses.
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/find/tt1375666", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"movie_results": []map[string]any{{"id": 27205}},
			"tv_results":    []map[string]any{},
		})
	})
	mux.HandleFunc("/movie/27205", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 27205, "title": "盗梦空间", "original_title": "Inception",
			"release_date": "2010-07-16", "vote_average": 8.4, "vote_count": 35000,
			"poster_path": "/abc.jpg", "overview": "梦境窃贼",
		})
	})
	// A TV entry reachable only by title, to exercise the search path.
	mux.HandleFunc("/search/tv", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("query"); !strings.EqualFold(got, "The Long Watch") {
			write(w, map[string]any{"results": []any{}})
			return
		}
		write(w, map[string]any{"results": []map[string]any{
			{"id": 4242, "name": "The Long Watch", "first_air_date": "2026-01-05"},
		}})
	})
	mux.HandleFunc("/tv/4242", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{
			"id": 4242, "name": "The Long Watch", "original_name": "The Long Watch",
			"first_air_date": "2026-01-05", "vote_average": 7.1, "vote_count": 120,
			"poster_path": "/tv.jpg", "overview": "一档节目",
		})
	})
	// Exact-title search must not accept a different title.
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"results": []map[string]any{
			{"id": 999, "title": "Something Else Entirely", "release_date": "2019-01-01"},
		}})
	})
	mux.HandleFunc("/movie/999", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"id": 999, "title": "Something Else Entirely", "release_date": "2019-01-01"})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New("test-key", "zh-CN")
	c.SetBaseURL(srv.URL)
	return c
}

func TestResolveByIMDbID(t *testing.T) {
	srv := fixtureServer(t)
	c := newTestClient(t, srv)

	e, err := c.Resolve(context.Background(), "tt1375666", "Inception.2010.1080p.BluRay")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 27205 || e.Type != "movie" {
		t.Fatalf("got id=%d type=%s, want 27205 movie", e.ID, e.Type)
	}
	if e.MatchedBy != "imdb" || e.Confidence != 1.0 {
		t.Errorf("matched_by=%s confidence=%v, want imdb 1.0", e.MatchedBy, e.Confidence)
	}
	if e.Year != 2010 || e.Title != "盗梦空间" {
		t.Errorf("year=%d title=%q, want 2010 盗梦空间", e.Year, e.Title)
	}
	if got := e.PosterURL(500); got != "https://image.tmdb.org/t/p/w500/abc.jpg" {
		t.Errorf("PosterURL = %q", got)
	}
	if e.URL() != "https://www.themoviedb.org/movie/27205" {
		t.Errorf("URL = %q", e.URL())
	}
}

// An id match must be accepted even when the release name's year disagrees,
// because the id was resolved from the detail page rather than guessed.
func TestResolveIDIgnoresTitleMismatch(t *testing.T) {
	srv := fixtureServer(t)
	c := newTestClient(t, srv)

	e, err := c.Resolve(context.Background(), "tt1375666", "Completely Unrelated Name 1999")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 27205 {
		t.Fatalf("got id=%d, want 27205", e.ID)
	}
}

func TestResolveByTitleTV(t *testing.T) {
	srv := fixtureServer(t)
	c := newTestClient(t, srv)

	e, err := c.Resolve(context.Background(), "", "The.Long.Watch.S01E05-E07.2026.2160p.WEB-DL.H265.DV.DDP5.1-BlackTV")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.ID != 4242 || e.Type != "tv" {
		t.Fatalf("got id=%d type=%s, want 4242 tv", e.ID, e.Type)
	}
	if e.MatchedBy != "title" {
		t.Errorf("matched_by = %s, want title", e.MatchedBy)
	}
	if e.Year != 2026 {
		t.Errorf("year = %d, want 2026", e.Year)
	}
}

// A title search that returns a different title must not be accepted.
func TestResolveTitleRequiresExactMatch(t *testing.T) {
	srv := fixtureServer(t)
	c := newTestClient(t, srv)

	_, err := c.Resolve(context.Background(), "", "Ring.Ring.2019.1080p.WEBRip")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
}

func TestResolveWithoutKey(t *testing.T) {
	c := New("", "zh-CN")
	_, err := c.Resolve(context.Background(), "tt1375666", "Inception.2010")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if c.Configured() {
		t.Error("Configured() = true, want false")
	}
}

// A v4 bearer token must go in the Authorization header, never the query.
func TestV4TokenUsesHeader(t *testing.T) {
	var gotAuth, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.URL.Query().Get("api_key")
		write(w, map[string]any{"movie_results": []map[string]any{{"id": 1}}, "tv_results": []any{}})
	}))
	defer srv.Close()

	// A v4 credential is a JWT: three base64 segments joined by two dots.
	c := New("eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJhYmMiLCJzdWIiOiJ4eXoifQ.abcdefghijklmnopqrstuvwxyz0123456789", "en-US")
	c.SetBaseURL(srv.URL)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/find/tt1", nil)
	var out map[string]any
	if err := c.get(context.Background(), "/find/tt1", req.URL.Query(), &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q, want Bearer prefix", gotAuth)
	}
	if gotKey != "" {
		t.Errorf("api_key leaked into query: %q", gotKey)
	}
}

func TestUnauthorizedKeyIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		write(w, map[string]any{"status_message": "Invalid API key"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Resolve(context.Background(), "tt1", "Inception.2010")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want a 401 message", err)
	}
}
