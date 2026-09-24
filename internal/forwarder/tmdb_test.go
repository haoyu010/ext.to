package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/scrape"
	"github.com/haoyu010/ext.to/internal/telegram"
	"github.com/haoyu010/ext.to/internal/tmdb"
)

// pointPosterCASAt redirects the TMDB image CDN at a fixture server, since
// poster URLs are built from a package-level base.
func pointPosterCASAt(t *testing.T, url string) {
	t.Helper()
	restore := tmdb.ImageBase
	tmdb.ImageBase = url
	t.Cleanup(func() { tmdb.ImageBase = restore })
}

// tmdbSite serves the endpoints the TMDB client calls for tt11564570, the
// IMDb id present on the captured Glass Onion detail page.
func tmdbSite(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/find/tt11564570", func(w http.ResponseWriter, r *http.Request) {
		writeJSONFixture(w, map[string]any{
			"movie_results": []map[string]any{{"id": 661374}},
			"tv_results":    []any{},
		})
	})
	mux.HandleFunc("/movie/661374", func(w http.ResponseWriter, r *http.Request) {
		writeJSONFixture(w, map[string]any{
			"id": 661374, "title": "利刃出鞘2", "original_title": "Glass Onion: A Knives Out Mystery",
			"release_date": "2022-11-23", "vote_average": 7.1, "vote_count": 533052,
			"poster_path": "/tmdb-poster.jpg", "overview": "贝诺瓦·布兰克前往希腊。",
		})
	})
	mux.HandleFunc("/tmdb-poster.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte(strings.Repeat("t", 2048)))
	})
	// Poster URLs carry a size prefix such as /w500, so serve any remaining
	// path with the same bytes.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".jpg") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte(strings.Repeat("t", 2048)))
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSONFixture(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// messageText returns the body of a captured send, whether it went out as a
// photo caption or as a plain message.
func messageText(rec capturedSend) string {
	if rec.form["caption"] != "" {
		return rec.form["caption"]
	}
	return rec.form["text"]
}

// unknownDetailSite serves a detail page whose IMDb id the fixture TMDB
// server does not know, so no match can be found.
func unknownDetailSite(t *testing.T) *httptest.Server {
	t.Helper()
	const page = `<html><body><ul class="detail-page-info-list">` +
		`<li><strong>Movie:</strong> <a href="/x/"><span>Unknown Film</span></a></li>` +
		`<li><strong>IMDb link:</strong> ` +
		`<a href="https://www.imdb.com/title/tt9999999/">9999999</a></li>` +
		`</ul></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "getTorrentMagnet") {
			_, _ = w.Write([]byte(`{"success":true,"url":"magnet:?xt=urn:btih:X"}`))
			return
		}
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// detailSite serves a detail page shaped like the real Glass Onion capture,
// including the "Movie:" label, the IMDb link and a page token.
func detailSite(t *testing.T, posterURL string) *httptest.Server {
	t.Helper()
	page := `<html><head>` +
		`<meta name="csrf-token" content="csrf-token-abcdef">` +
		`<script>window.pageToken = 'page-token-xyz';</script>` +
		`</head><body><div class="row movie-info"><ul class="detail-page-info-list">` +
		`<li><strong>Movie:</strong> <a href="/glass-onion-m128650/">` +
		`<span>Glass Onion: A Knives Out Mystery</span></a></li>` +
		`<li><strong>IMDb link:</strong> ` +
		`<a rel="nofollow" href="https://www.imdb.com/title/tt11564570/">11564570</a></li>` +
		`</ul></div>` +
		`<img class="detail-torrent-image" src="` + posterURL + `">` +
		`</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getTorrentMagnet"):
			_, _ = w.Write([]byte(`{"success":true,"url":"magnet:?xt=urn:btih:CAFE"}`))
		default:
			_, _ = w.Write([]byte(page))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPublishEnrichesWithTMDB covers the enrichment path end to end: the IMDb
// id on the detail page resolves the TMDB entry, the caption uses the TMDB
// title and rating, the TMDB poster is uploaded, and the match is recorded.
func TestPublishEnrichesWithTMDB(t *testing.T) {
	movies := tmdbSite(t)
	// The poster is served by the same fixture host as the API.
	pointPosterCASAt(t, movies.URL)
	site := detailSite(t, "https://tracker.example/poster.jpg")
	restore := scrape.BaseURL
	scrape.BaseURL = site.URL
	defer func() { scrape.BaseURL = restore }()

	tgSrv, sent := newMockTelegram(t, false)
	restoreTG := telegram.APIBase
	telegram.APIBase = tgSrv.URL
	defer func() { telegram.APIBase = restoreTG }()

	f, state, settings := newTestForwarder(t, func(s *config.Settings) {
		s.WithPoster = true
		s.WithMagnet = true
		s.TMDBKey = "test-key"
		s.PosterSource = config.PosterSourceTMDB
		s.Template = "{tmdb_title}|{tmdb_year}|{tmdb_rating}|{tmdb_url}|{tmdb_id}|{tmdb_type}"
	})
	tmdbClient := tmdb.New(settings.TMDBKey, settings.TMDBLang)
	tmdbClient.SetBaseURL(movies.URL)

	client, err := scrape.New(settings)
	if err != nil {
		t.Fatalf("scrape.New: %v", err)
	}
	item := scrape.Item{ID: 4242, Slug: "x-4242", Title: "Some.Release.Name.2022.720p", URL: site.URL + "/x-4242/"}

	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdbClient, settings, item, 0); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if len(*sent) != 1 {
		t.Fatalf("expected one message, got %d", len(*sent))
	}
	rec := (*sent)[0]
	if rec.method != "sendPhoto" {
		t.Errorf("expected sendPhoto, got %s", rec.method)
	}
	// The TMDB poster must win over the tracker image.
	if len(rec.photo) != 2048 {
		t.Errorf("photo bytes = %d, want the 2048 byte TMDB poster", len(rec.photo))
	}
	caption := messageText(rec)
	for _, want := range []string{
		"利刃出鞘2", "2022", "7.1",
		"https://www.themoviedb.org/movie/661374", "661374", "movie",
	} {
		if !strings.Contains(caption, want) {
			t.Errorf("caption missing %q: %q", want, caption)
		}
	}

	recs := state.Recent(10, "sent")
	if len(recs) != 1 {
		t.Fatalf("expected one sent record, got %d", len(recs))
	}
	if !recs[0].TMDBMatched || recs[0].TMDBID != 661374 || recs[0].TMDBTitle != "利刃出鞘2" {
		t.Errorf("tmdb match not recorded: %+v", recs[0])
	}
}

// TestPublishTMDBOnlySkipsUnmatched verifies that enabling the strict mode
// drops releases with no TMDB entry instead of posting them.
func TestPublishTMDBOnlySkipsUnmatched(t *testing.T) {
	movies := tmdbSite(t)
	srv := unknownDetailSite(t)

	restore := scrape.BaseURL
	scrape.BaseURL = srv.URL
	defer func() { scrape.BaseURL = restore }()

	tgSrv, sent := newMockTelegram(t, false)
	restoreTG := telegram.APIBase
	telegram.APIBase = tgSrv.URL
	defer func() { telegram.APIBase = restoreTG }()

	f, state, settings := newTestForwarder(t, func(s *config.Settings) {
		s.TMDBKey = "test-key"
		s.TMDBOnly = true
		s.WithPoster = false
		s.WithMagnet = false
	})
	tmdbClient := tmdb.New(settings.TMDBKey, settings.TMDBLang)
	tmdbClient.SetBaseURL(movies.URL)

	client, _ := scrape.New(settings)
	item := scrape.Item{ID: 77, Slug: "x-77", Title: "Unknown.Film.1999.1080p", URL: srv.URL + "/x-77/"}

	err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdbClient, settings, item, 0)
	if err == nil {
		t.Fatal("expected a no-match error under TMDBOnly")
	}
	if len(*sent) != 0 {
		t.Errorf("nothing should be posted, got %d message(s)", len(*sent))
	}
	if recs := state.Recent(10, "sent"); len(recs) != 0 {
		t.Errorf("nothing should be recorded as sent: %+v", recs)
	}
}

// TestPublishSurvivesTMDBOutage ensures TMDB being unreachable degrades the
// caption rather than blocking delivery.
func TestPublishSurvivesTMDBOutage(t *testing.T) {
	site := detailSite(t, "https://tracker.example/poster.jpg")
	restore := scrape.BaseURL
	scrape.BaseURL = site.URL
	defer func() { scrape.BaseURL = restore }()

	tgSrv, sent := newMockTelegram(t, false)
	restoreTG := telegram.APIBase
	telegram.APIBase = tgSrv.URL
	defer func() { telegram.APIBase = restoreTG }()

	f, _, settings := newTestForwarder(t, func(s *config.Settings) {
		s.TMDBKey = "test-key"
		s.WithPoster = false
		s.WithMagnet = false
		s.Template = "{tmdb_title}|{tmdb_year}"
	})
	// Point TMDB at a closed server to simulate an outage.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	tmdbClient := tmdb.New(settings.TMDBKey, settings.TMDBLang)
	tmdbClient.SetBaseURL(deadURL)

	client, _ := scrape.New(settings)
	item := scrape.Item{ID: 5, Slug: "x-5", Title: "Fallback.Name.2020.1080p", URL: site.URL + "/x-5/"}

	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdbClient, settings, item, 0); err != nil {
		t.Fatalf("publish must survive a tmdb outage: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected one message, got %d", len(*sent))
	}
	// With no match the TMDB placeholders fall back to the torrent title.
	caption := messageText((*sent)[0])
	if !strings.Contains(caption, "Fallback.Name.2020.1080p") {
		t.Errorf("expected the torrent title to stand in: %q", caption)
	}
}

// TestUnmatchedIsSkipNotFailure checks the classification the run loop relies
// on: an unmatched torrent under TMDBOnly is reported with the no-match
// marker, so the cycle counts it as skipped and keeps going instead of
// treating it as a delivery failure.
func TestUnmatchedIsSkipNotFailure(t *testing.T) {
	site := mixedDetailSite(t)
	restore := scrape.BaseURL
	scrape.BaseURL = site.URL
	defer func() { scrape.BaseURL = restore }()

	tgSrv, sent := newMockTelegram(t, false)
	restoreTG := telegram.APIBase
	telegram.APIBase = tgSrv.URL
	defer func() { telegram.APIBase = restoreTG }()

	f, _, settings := newTestForwarder(t, func(s *config.Settings) {
		s.TMDBKey = "test-key"
		s.TMDBOnly = true
		s.WithPoster = false
		s.WithMagnet = false
	})
	tmdbClient := tmdb.New(settings.TMDBKey, settings.TMDBLang)
	tmdbClient.SetBaseURL(tmdbSite(t).URL)
	client, _ := scrape.New(settings)

	err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdbClient, settings,
		scrape.Item{ID: 3, Slug: "b-3", Title: "Unknown.Thing.1999", URL: site.URL + "/unknown/b-3/"}, 0)
	if !errors.Is(err, errNoMatch) {
		t.Fatalf("err = %v, want errNoMatch", err)
	}
	if len(*sent) != 0 {
		t.Errorf("an unmatched torrent must not be posted, got %d", len(*sent))
	}

	// A matched torrent under the same configuration still delivers, proving
	// the skip does not poison the rest of the cycle.
	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdbClient, settings,
		scrape.Item{ID: 2, Slug: "g-2", Title: "Glass.Onion.2022.720p", URL: site.URL + "/g-2/"}, 0); err != nil {
		t.Fatalf("matched torrent should be sent: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected the matched torrent to be sent, got %d", len(*sent))
	}
}

// mixedDetailSite serves two detail pages under one host: /known/ carries an
// IMDb id the fixture TMDB server resolves, /unknown/ carries one it does not.
// A single host lets one test exercise both outcomes with identical settings.
func mixedDetailSite(t *testing.T) *httptest.Server {
	t.Helper()
	const infoList = `<ul class="detail-page-info-list">`
	known := `<html><head><meta name="csrf-token" content="csrf-token-abcdef">` +
		`<script>window.pageToken = 'page-token-xyz';</script></head><body>` +
		infoList +
		`<li><strong>Movie:</strong> <a href="/g/"><span>Glass Onion: A Knives Out Mystery</span></a></li>` +
		`<li><strong>IMDb link:</strong> <a href="https://www.imdb.com/title/tt11564570/">x</a></li>` +
		`</ul></body></html>`
	unknown := `<html><body>` + infoList +
		`<li><strong>Movie:</strong> <a href="/u/"><span>Unknown Film</span></a></li>` +
		`<li><strong>IMDb link:</strong> <a href="https://www.imdb.com/title/tt9999999/">x</a></li>` +
		`</ul></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getTorrentMagnet"):
			_, _ = w.Write([]byte(`{"success":true,"url":"magnet:?xt=urn:btih:CAFE"}`))
		case strings.HasPrefix(r.URL.Path, "/unknown"):
			_, _ = w.Write([]byte(unknown))
		default:
			_, _ = w.Write([]byte(known))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
