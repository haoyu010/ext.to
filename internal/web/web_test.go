package web

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/forwarder"
	"github.com/haoyu010/ext.to/internal/store"
	"github.com/haoyu010/ext.to/internal/tmdb"
)

const maskSample = "abcdefghijklmnop********wxyz"

// newTestServer builds a Server over a temporary config.
func newTestServer(t *testing.T, mutate func(*config.Settings)) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if mutate != nil {
		s := cfg.Get()
		mutate(&s)
		if err := cfg.Update(s); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	state, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return New(Options{
		Config:  cfg,
		Store:   state,
		Forward: forwarder.New(cfg, state, log.New(io.Discard, "", 0)),
		Logger:  log.New(io.Discard, "", 0),
	})
}

func putSettings(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handlePutSettings(rec, req)
	return rec
}

// TMDBKey is masked in the settings response, so the UI echoes the mask back on
// save. Treating that as a new key would overwrite the real one with "********"
// and break every later lookup, so the stored value has to survive.
func TestPutSettingsPreservesMaskedTMDBKey(t *testing.T) {
	s := newTestServer(t, func(s *config.Settings) {
		s.TMDBKey = maskSample
	})

	rec := putSettings(t, s, `{"tmdb_key":"ab********wxyz","tmdb_lang":"zh-CN"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := s.cfg.Get().TMDBKey; got != maskSample {
		t.Errorf("TMDBKey = %q, want the stored key to survive a masked save", got)
	}
}

// The same must hold for the other masked secrets.
func TestPutSettingsPreservesMaskedSecrets(t *testing.T) {
	s := newTestServer(t, func(s *config.Settings) {
		s.TMDBKey = maskSample
		s.BotToken = "123456:AAHsecret-token-value"
		s.Clearance = "clearance-value-that-is-long"
		s.Session = "session-value-long"
	})

	rec := putSettings(t, s, `{
		"tmdb_key":"ab********wxyz",
		"bot_token":"1234********alue",
		"clearance":"clea********long",
		"session":"sess********long"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	got := s.cfg.Get()
	if got.TMDBKey != maskSample {
		t.Errorf("TMDBKey = %q", got.TMDBKey)
	}
	if got.BotToken != "123456:AAHsecret-token-value" {
		t.Errorf("BotToken = %q", got.BotToken)
	}
	if got.Clearance != "clearance-value-that-is-long" {
		t.Errorf("Clearance = %q", got.Clearance)
	}
	if got.Session != "session-value-long" {
		t.Errorf("Session = %q", got.Session)
	}
}

// Testing a key before saving it is the whole point of the lookup box, so the
// on-screen key must be used rather than the stored one.
func TestLookupErrorTextDistinguishesCauses(t *testing.T) {
	missing := lookupErrorText(tmdb.ErrNotConfigured)
	if !strings.Contains(missing, "API Key") {
		t.Errorf("a missing key should point at the key field, got %q", missing)
	}
	noMatch := lookupErrorText(tmdb.ErrNoMatch)
	if !strings.Contains(noMatch, "没有匹配到") {
		t.Errorf("an unmatched title should say so, got %q", noMatch)
	}
	if missing == noMatch {
		t.Error("the two failures must not share wording")
	}
	// Anything else keeps the underlying detail instead of hiding it.
	other := lookupErrorText(errors.New("boom"))
	if !strings.Contains(other, "boom") {
		t.Errorf("an unexpected error should keep its detail, got %q", other)
	}
}

// A rejected key is the single most likely failure when setting TMDB up, so it
// must name its own fix instead of surfacing the upstream English.
func TestLookupErrorTextLocalizesKeyFailures(t *testing.T) {
	bad := lookupErrorText(fmt.Errorf("%w: 401", tmdb.ErrUnauthorized))
	if !strings.Contains(bad, "401") {
		t.Errorf("a rejected key should say so, got %q", bad)
	}
	if strings.Contains(bad, "rejected the api key") {
		t.Errorf("the upstream English wording must not reach the panel, got %q", bad)
	}

	throttled := lookupErrorText(fmt.Errorf("%w: 429", tmdb.ErrRateLimited))
	if !strings.Contains(throttled, "429") {
		t.Errorf("a throttle should be named, got %q", throttled)
	}
	if throttled == bad {
		t.Error("a rejected key and a throttle need different advice")
	}
}
