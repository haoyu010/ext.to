package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultTMDBFieldsAreUsable guards the defaults a fresh install gets.
// zh-CN is chosen because the dashboard and its audience are Chinese.
func TestDefaultTMDBFieldsAreUsable(t *testing.T) {
	d := Default()
	if d.TMDBLang != "zh-CN" {
		t.Errorf("TMDBLang = %q, want zh-CN", d.TMDBLang)
	}
	if d.PosterSource != PosterSourceAuto {
		t.Errorf("PosterSource = %q, want %q", d.PosterSource, PosterSourceAuto)
	}
	if d.TMDBOnly {
		t.Error("TMDBOnly should default to false so enrichment is opt-in")
	}
	if err := d.Validate(); err != nil {
		t.Errorf("defaults must validate without a TMDB key: %v", err)
	}
}

func TestValidateRejectsUnknownPosterSource(t *testing.T) {
	s := Default()
	s.PosterSource = "nope"
	if err := s.Validate(); err == nil {
		t.Fatal("expected an error for an unknown poster source")
	}
}

// Enabling the strict mode without a key would silently skip every torrent,
// so it must be rejected up front.
func TestValidateRejectsTMDBOnlyWithoutKey(t *testing.T) {
	s := Default()
	s.TMDBOnly = true
	if err := s.Validate(); err == nil {
		t.Fatal("expected an error for tmdb_only without an api key")
	}
	s.TMDBKey = "key"
	if err := s.Validate(); err != nil {
		t.Errorf("tmdb_only with a key should validate: %v", err)
	}
}

// Forcing the tracker poster means TMDB artwork is never requested, so the
// strict mode no longer needs a key.
func TestValidateAllowsTMDBOnlyWhenOnlyPosterSourceIsTracker(t *testing.T) {
	s := Default()
	s.PosterSource = PosterSourceTracker
	if err := s.Validate(); err != nil {
		t.Errorf("tracker-only poster source should not require a key: %v", err)
	}
}

func TestValidateRejectsEmptyTMDBOnlyWithoutGating(t *testing.T) {
	// An unknown poster source is reported before the key requirement, so the
	// operator fixes the more specific problem first.
	s := Default()
	s.PosterSource = "bogus"
	s.TMDBOnly = true
	err := s.Validate()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), PosterSourceAuto) {
		t.Errorf("expected the poster source error first, got %v", err)
	}
}

// The key must never be returned in plain text to the dashboard.
func TestMaskedHidesTMDBKey(t *testing.T) {
	s := Default()
	s.TMDBKey = "super-secret-tmdb-key"
	m := s.Masked()
	if strings.Contains(m.TMDBKey, "secret") {
		t.Errorf("Masked leaked the tmdb key: %q", m.TMDBKey)
	}
	if m.TMDBKey == "" {
		t.Error("Masked should show a placeholder so the UI knows a key is stored")
	}
	if m.TMDBLang != s.TMDBLang {
		t.Error("non-secret fields must survive masking")
	}
}

func TestTMDBFieldsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	next := st.Get()
	next.TMDBKey = "abc123"
	next.TMDBLang = "en-US"
	next.TMDBOnly = true
	next.PosterSource = PosterSourceTMDB
	if err := st.Update(next); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reopened, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := reopened.Get()
	if got.TMDBKey != "abc123" || got.TMDBLang != "en-US" ||
		!got.TMDBOnly || got.PosterSource != PosterSourceTMDB {
		t.Errorf("tmdb settings did not persist: %+v", got)
	}
}
