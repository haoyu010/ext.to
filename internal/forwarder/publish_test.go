package forwarder

import (
	"context"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/scrape"
	"github.com/haoyu010/ext.to/internal/store"
	"github.com/haoyu010/ext.to/internal/telegram"
	"github.com/haoyu010/ext.to/internal/tmdb"
)

// capturedSend records one request received by the mock Telegram server.
type capturedSend struct {
	method  string
	form    map[string]string
	photo   []byte
	isPhoto bool
}

// newMockTelegram starts a server that mimics the Bot API endpoints the
// forwarder uses and records what was sent. When rejectMarkup is true the
// server rejects messages that carry a parse_mode, imitating Telegram's
// response to malformed HTML entities.
func newMockTelegram(t *testing.T, rejectMarkup bool) (*httptest.Server, *[]capturedSend) {
	t.Helper()
	var got []capturedSend
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := capturedSend{form: map[string]string{}}
		rec.method = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

		ctype := r.Header.Get("Content-Type")
		if strings.HasPrefix(ctype, "multipart/form-data") {
			rec.isPhoto = true
			_, params, err := mime.ParseMediaType(ctype)
			if err != nil {
				t.Errorf("parse content type: %v", err)
				return
			}
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Errorf("read multipart: %v", err)
					return
				}
				b, _ := io.ReadAll(part)
				if part.FileName() != "" {
					rec.photo = b
				} else {
					rec.form[part.FormName()] = string(b)
				}
			}
		} else {
			_ = r.ParseForm()
			for k := range r.Form {
				rec.form[k] = r.Form.Get(k)
			}
		}
		got = append(got, rec)

		w.Header().Set("Content-Type", "application/json")
		if rejectMarkup && rec.form["parse_mode"] != "" {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,` +
				`"description":"Bad Request: can't parse entities: Unclosed start tag"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":-100123}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// fixtureSite serves a listing page, a detail page carrying a pageToken, a
// signed magnet endpoint and a poster image.
func fixtureSite(t *testing.T, posterBytes int) *httptest.Server {
	t.Helper()
	var site *httptest.Server
	site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "getTorrentMagnet"):
			_, _ = w.Write([]byte(`{"success":true,` +
				`"url":"magnet:?xt=urn:btih:DEADBEEF\u0026tr=udp:\/\/tracker.example:6969"}`))
		case strings.HasPrefix(r.URL.Path, "/poster"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte(strings.Repeat("p", posterBytes)))
		case strings.HasPrefix(r.URL.Path, "/torrent-page"):
			_, _ = w.Write([]byte(`<html><head>` +
				`<meta name="csrf-token" content="csrf-value-1234567890">` +
				`<script>window.pageToken = 'page-token-abc';</script>` +
				`</head><body>` +
				`<img class="detail-torrent-image" src="` + site.URL + `/poster.jpg">` +
				`</body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	return site
}

// newTestForwarder builds a forwarder backed by a temporary data directory.
func newTestForwarder(t *testing.T, mutate func(*config.Settings)) (*Forwarder, *store.Store, config.Settings) {
	t.Helper()
	dir := t.TempDir()
	cfgStore, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	s := cfgStore.Get()
	s.BotToken = "1:test"
	s.ChatID = "-100123"
	s.Clearance = "cookie"
	s.Enabled = false
	s.WithPoster = false
	s.WithMagnet = false
	if mutate != nil {
		mutate(&s)
	}
	if err := cfgStore.Update(s); err != nil {
		t.Fatalf("Update settings: %v", err)
	}
	state, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return New(cfgStore, state, log.New(io.Discard, "", 0)), state, cfgStore.Get()
}

// TestPublishSendsPhotoWithCaptionAndMagnet covers the full delivery path: the
// poster is uploaded as multipart, the caption is HTML-escaped, and the magnet
// resolved from the detail page ends up in the message with its tracker URLs
// unescaped.
func TestPublishSendsPhotoWithCaptionAndMagnet(t *testing.T) {
	site := fixtureSite(t, 4096)
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
		s.Template = "<b>{title}</b>\n{magnet}\n{size}"
	})
	client, err := scrape.New(settings)
	if err != nil {
		t.Fatalf("scrape.New: %v", err)
	}

	item := scrape.Item{
		ID:       4242,
		Slug:     "test-title-4242",
		Title:    "Test & Title",
		URL:      site.URL + "/torrent-page/",
		Size:     "1.5 GB",
		SizeMB:   1536,
		Category: "Movies - Highres Movies",
	}
	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdb.New("", settings.TMDBLang), settings, item, 0); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if len(*sent) != 1 {
		t.Fatalf("expected exactly one Telegram request, got %d", len(*sent))
	}
	rec := (*sent)[0]
	if rec.method != "sendPhoto" {
		t.Errorf("expected sendPhoto, got %s", rec.method)
	}
	if !rec.isPhoto || len(rec.photo) != 4096 {
		t.Errorf("poster bytes not forwarded: isPhoto=%v bytes=%d", rec.isPhoto, len(rec.photo))
	}
	if rec.form["chat_id"] != "-100123" {
		t.Errorf("wrong chat_id: %q", rec.form["chat_id"])
	}
	if rec.form["parse_mode"] != "HTML" {
		t.Errorf("expected HTML parse mode, got %q", rec.form["parse_mode"])
	}
	caption := rec.form["caption"]
	if !strings.Contains(caption, "Test &amp; Title") {
		t.Errorf("caption should escape the title: %q", caption)
	}
	if !strings.Contains(caption, "magnet:?xt=urn:btih:DEADBEEF") {
		t.Errorf("caption should contain the resolved magnet: %q", caption)
	}
	if strings.Contains(caption, `\/`) {
		t.Errorf("caption still contains escaped slashes: %q", caption)
	}
	if !strings.Contains(caption, "udp://tracker.example:6969") {
		t.Errorf("tracker URL was not restored: %q", caption)
	}
	if !strings.Contains(caption, "1.5 GB") {
		t.Errorf("caption should contain the size: %q", caption)
	}

	recs := state.Recent(10, "sent")
	if len(recs) != 1 || recs[0].ID != item.ID {
		t.Fatalf("torrent was not recorded as sent: %+v", recs)
	}
	if recs[0].Magnet == "" {
		t.Error("the resolved magnet should be stored in the record")
	}
}

// TestPublishFallsBackToPlainTextWhenCaptionIsInvalid ensures a title
// containing stray markup does not lose the post.
func TestPublishFallsBackToPlainText(t *testing.T) {
	site := fixtureSite(t, 8)
	restore := scrape.BaseURL
	scrape.BaseURL = site.URL
	defer func() { scrape.BaseURL = restore }()

	tgSrv, sent := newMockTelegram(t, true)
	restoreTG := telegram.APIBase
	telegram.APIBase = tgSrv.URL
	defer func() { telegram.APIBase = restoreTG }()

	f, _, settings := newTestForwarder(t, func(s *config.Settings) {
		s.WithPoster = false
		s.WithMagnet = false
		s.Template = "<b>{title}"
	})
	client, _ := scrape.New(settings)

	item := scrape.Item{ID: 7, Slug: "x-7", Title: "Bad <tag>", URL: site.URL + "/torrent-page/"}
	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdb.New("", settings.TMDBLang), settings, item, 0); err != nil {
		t.Fatalf("publish should retry without markup: %v", err)
	}
	if len(*sent) < 2 {
		t.Fatalf("expected a retry after the entity error, got %d request(s)", len(*sent))
	}
	last := (*sent)[len(*sent)-1]
	if last.form["parse_mode"] != "" {
		t.Errorf("the retry should drop parse_mode, got %q", last.form["parse_mode"])
	}
}

// TestPublishWithoutMagnetStillPosts checks that a magnet failure only logs a
// warning: the post must still be delivered.
func TestPublishWithoutMagnetStillPosts(t *testing.T) {
	// A detail page without a pageToken makes magnet resolution fail.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>no token here</body></html>`))
	}))
	t.Cleanup(site.Close)
	restore := scrape.BaseURL
	scrape.BaseURL = site.URL
	defer func() { scrape.BaseURL = restore }()

	tgSrv, sent := newMockTelegram(t, false)
	restoreTG := telegram.APIBase
	telegram.APIBase = tgSrv.URL
	defer func() { telegram.APIBase = restoreTG }()

	f, state, settings := newTestForwarder(t, func(s *config.Settings) {
		s.WithMagnet = true
		s.WithPoster = false
	})
	client, _ := scrape.New(settings)

	item := scrape.Item{ID: 11, Slug: "y-11", Title: "No Magnet", URL: site.URL + "/y-11/"}
	if err := f.publish(context.Background(), client, telegram.New(settings.BotToken),
		tmdb.New("", settings.TMDBLang), settings, item, 0); err != nil {
		t.Fatalf("publish should succeed without a magnet: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected one message, got %d", len(*sent))
	}
	if recs := state.Recent(10, "sent"); len(recs) != 1 {
		t.Errorf("torrent should still be recorded as sent: %+v", recs)
	}
}
