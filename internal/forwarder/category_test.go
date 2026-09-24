package forwarder

import (
	"context"
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

// regionTMDB serves two TV entries with the metadata the classification rules
// match on: a Chinese cartoon (genre 16, origin CN) and a Korean drama
// (genre 18, origin KR). The tracker lists both under the same 动漫 category,
// which is exactly why the distinction cannot be made before the TMDB match.
func regionTMDB(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/find/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/find/")
		switch id {
		case "tt1000001":
			writeJSONFixture(w, map[string]any{"movie_results": []any{}, "tv_results": []map[string]any{{"id": 9001}}})
		case "tt1000002":
			writeJSONFixture(w, map[string]any{"movie_results": []any{}, "tv_results": []map[string]any{{"id": 9002}}})
		default:
			writeJSONFixture(w, map[string]any{"movie_results": []any{}, "tv_results": []any{}})
		}
	})
	mux.HandleFunc("/tv/9001", func(w http.ResponseWriter, r *http.Request) {
		writeJSONFixture(w, map[string]any{
			"id": 9001, "name": "国产动画", "original_name": "国产动画",
			"first_air_date": "2024-01-05", "vote_average": 8.0, "vote_count": 100,
			"original_language": "zh", "origin_country": []string{"CN"},
			"genres": []map[string]any{{"id": 16, "name": "动画"}, {"id": 10759, "name": "动作冒险"}},
		})
	})
	mux.HandleFunc("/tv/9002", func(w http.ResponseWriter, r *http.Request) {
		writeJSONFixture(w, map[string]any{
			"id": 9002, "name": "韩国剧集", "original_name": "한국 드라마",
			"first_air_date": "2023-04-01", "vote_average": 7.5, "vote_count": 50,
			"original_language": "ko", "origin_country": []string{"KR"},
			"genres": []map[string]any{{"id": 18, "name": "剧情"}, {"id": 10766, "name": "肥皂剧"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// regionSite serves the detail pages the regionTMDB ids appear on, each
// carrying the IMDb link the resolver prefers.
func regionSite(t *testing.T) *httptest.Server {
	t.Helper()
	page := func(imdb, title string) string {
		return `<html><body><ul class="detail-page-info-list">` +
			`<li><strong>TV Show:</strong> <a href="/x/"><span>` + title + `</span></a></li>` +
			`<li><strong>IMDb link:</strong> ` +
			`<a href="https://www.imdb.com/title/` + imdb + `/">x</a></li></ul></body></html>`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getTorrentMagnet"):
			_, _ = w.Write([]byte(`{"success":true,"url":"magnet:?xt=urn:btih:CAFE"}`))
		case strings.HasPrefix(r.URL.Path, "/cn"):
			_, _ = w.Write([]byte(page("tt1000001", "国产动画")))
		case strings.HasPrefix(r.URL.Path, "/unknown"):
			// An id the fixture TMDB does not know, so no match is possible.
			_, _ = w.Write([]byte(page("tt9999999", "未知条目")))
		default:
			_, _ = w.Write([]byte(page("tt1000002", "韩国剧集")))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPublishKeepsChineseAnimationAndDropsKoreanDrama is the behaviour the
// operator asked for: with the shipped rules and blacklist, Chinese animation
// is forwarded while a Korean drama from the same tracker category is not.
func TestPublishKeepsChineseAnimationAndDropsKoreanDrama(t *testing.T) {
	movies := regionTMDB(t)
	site := regionSite(t)
	restore := scrape.BaseURL
	scrape.BaseURL = site.URL
	defer func() { scrape.BaseURL = restore }()

	tgSrv, sent := newMockTelegram(t, false)
	restoreTG := telegram.APIBase
	telegram.APIBase = tgSrv.URL
	defer func() { telegram.APIBase = restoreTG }()

	f, state, settings := newTestForwarder(t, func(s *config.Settings) {
		s.TMDBKey = "test-key"
		s.WithPoster = false
		s.WithMagnet = false
		s.Template = "{category_tmdb}|{tmdb_title}"
	})
	moviesClient := tmdb.New(settings.TMDBKey, settings.TMDBLang)
	moviesClient.SetBaseURL(movies.URL)
	client, err := scrape.New(settings)
	if err != nil {
		t.Fatalf("scrape.New: %v", err)
	}
	rf := chineseRules(t)

	// The Chinese cartoon is kept.
	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		moviesClient, settings, rf,
		scrape.Item{ID: 101, Slug: "cn-101", Title: "[剧集] 国产动画 (2024) S01E01",
			URL: site.URL + "/cn/cn-101/"}, 0); err != nil {
		t.Fatalf("Chinese animation should be forwarded: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected one message, got %d", len(*sent))
	}
	if caption := messageText((*sent)[0]); !strings.Contains(caption, "国漫") {
		t.Errorf("caption should name the classified category: %q", caption)
	}

	// The Korean drama is dropped, and the reason names the category.
	err = f.publish(context.Background(), client, telegram.New(settings.BotToken),
		moviesClient, settings, rf,
		scrape.Item{ID: 102, Slug: "kr-102", Title: "[剧集] 韩国剧集 (2023) S01E01",
			URL: site.URL + "/kr/kr-102/"}, 0)
	if !errors.Is(err, errCategoryBlocked) {
		t.Fatalf("err = %v, want a category block", err)
	}
	if !strings.Contains(err.Error(), "日韩剧") {
		t.Errorf("the reason should name the category, got %v", err)
	}
	if len(*sent) != 1 {
		t.Errorf("nothing else should be posted, got %d message(s)", len(*sent))
	}

	// Both classifications are recorded, so the history can explain the skip.
	recs := state.Recent(10, "")
	byID := map[int]string{}
	for _, r := range recs {
		byID[r.ID] = r.RuleCategory
	}
	if byID[101] != "国漫" {
		t.Errorf("record 101 category = %q, want 国漫", byID[101])
	}
	if byID[102] != "日韩剧" {
		t.Errorf("record 102 category = %q, want 日韩剧", byID[102])
	}
}

// A tracker category cannot narrow a channel, but with no rules configured the
// forwarder must keep posting everything it always did.
func TestPublishWithoutRulesKeepsEverything(t *testing.T) {
	movies := regionTMDB(t)
	site := regionSite(t)
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
	})
	moviesClient := tmdb.New(settings.TMDBKey, settings.TMDBLang)
	moviesClient.SetBaseURL(movies.URL)
	client, _ := scrape.New(settings)

	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		moviesClient, settings, disabledRules(t),
		scrape.Item{ID: 103, Slug: "kr-103", Title: "[剧集] 韩国剧集 (2023) S01E01",
			URL: site.URL + "/kr/kr-103/"}, 0); err != nil {
		t.Fatalf("a Korean drama must still be posted with no rules configured: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected one message, got %d", len(*sent))
	}
}

// Classification needs a TMDB match, so a release that cannot be matched must
// be skipped rather than posted unverified: it might belong to any region, and
// guessing would defeat the point of the filter. The wording has to differ from
// a blacklist hit, because one is an unverifiable release and the other is a
// filter doing its job.
func TestUnmatchedReleaseIsSkippedWhenRulesAreOn(t *testing.T) {
	movies := regionTMDB(t)
	site := regionSite(t)
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
	})
	moviesClient := tmdb.New(settings.TMDBKey, settings.TMDBLang)
	moviesClient.SetBaseURL(movies.URL)
	client, _ := scrape.New(settings)

	// This detail page carries an id the fixture TMDB does not know.
	err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		moviesClient, settings, chineseRules(t),
		scrape.Item{ID: 104, Slug: "unk-104", Title: "[剧集] 未知条目 (2024) S01E01",
			URL: site.URL + "/unknown/unk-104/"}, 0)
	if !errors.Is(err, errCategoryBlocked) {
		t.Fatalf("err = %v, want a skip", err)
	}
	if !strings.Contains(err.Error(), "TMDB") {
		t.Errorf("the wording should say the match is missing, got %v", err)
	}
	if len(*sent) != 0 {
		t.Errorf("an unverifiable release must not be posted, got %d", len(*sent))
	}
}
