package config

import (
	"path/filepath"
	"testing"
)

func TestParseSizeMB(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"662.42 MB", 662.42, true},
		{"1.43 GB", 1464.32, true},
		{"5.7 GB", 5836.8, true},
		{"900 KB", 0.87890625, true},
		{"2 TB", 2097152, true},
		{"1.5 GiB", 1536, true},
		{"", 0, false},
		{"oops", 0, false},
		{"12 XB", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseSizeMB(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseSizeMB(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if diff := got - tc.want; diff > 0.01 || diff < -0.01 {
			t.Errorf("ParseSizeMB(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestStorePersistsAndValidates covers save/load round-tripping and the
// validation rules that protect the forwarder from a bad configuration.
func TestStorePersistsAndValidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Get().AdminUser != "admin" {
		t.Errorf("defaults not applied, got admin user %q", st.Get().AdminUser)
	}

	next := st.Get()
	next.IntervalSeconds = 10 // below the minimum, should be clamped
	next.MaxPages = 500       // above the maximum, should be clamped
	next.Categories = []int{CatMovies, CatAnime}
	if err := st.Update(next); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := st.Get()
	if got.IntervalSeconds != 60 {
		t.Errorf("interval should clamp to 60, got %d", got.IntervalSeconds)
	}
	if got.MaxPages != 20 {
		t.Errorf("max pages should clamp to 20, got %d", got.MaxPages)
	}

	reopened, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reopened.Get().Categories) != 2 {
		t.Errorf("categories did not persist: %+v", reopened.Get().Categories)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	base := Default()

	bad := base
	bad.Age = 9
	if err := bad.Validate(); err == nil {
		t.Error("expected an error for an out-of-range age window")
	}

	bad = base
	bad.Categories = []int{999}
	if err := bad.Validate(); err == nil {
		t.Error("expected an error for an unknown category id")
	}

	bad = base
	bad.Include = []string{"[unclosed"}
	if err := bad.Validate(); err == nil {
		t.Error("expected an error for an invalid include pattern")
	}

	bad = base
	bad.MinSizeMB, bad.MaxSizeMB = 5000, 100
	if err := bad.Validate(); err == nil {
		t.Error("expected an error when the minimum exceeds the maximum")
	}

	// Enabling monitoring without credentials must be rejected.
	bad = base
	bad.Enabled = true
	if err := bad.Validate(); err == nil {
		t.Error("expected an error when enabling without a bot token")
	}

	bad = base
	bad.Enabled = true
	bad.BotToken = "123:abc"
	bad.ChatID = "-100123"
	if err := bad.Validate(); err == nil {
		t.Error("expected an error when enabling without a clearance cookie")
	}

	bad.Clearance = "cookie-value"
	if err := bad.Validate(); err != nil {
		t.Errorf("a fully configured settings should validate, got %v", err)
	}
}

// TestMaskedHidesSecrets ensures the API never echoes raw credentials.
func TestMaskedHidesSecrets(t *testing.T) {
	s := Default()
	s.BotToken = "123456789:AAF-verysecretvalue"
	s.Clearance = "8i.r79AIsNCkuz37sENsZndkNYnh1JjoJRDyvvqvY9o"
	s.AdminPassword = "hunter2"

	m := s.Masked()
	if m.BotToken == s.BotToken || m.Clearance == s.Clearance {
		t.Error("Masked must not return the raw secret")
	}
	if m.AdminPassword != "" {
		t.Error("Masked must drop the admin password")
	}
	if len(m.BotToken) == 0 || m.BotToken[0] != s.BotToken[0] {
		t.Error("masking should keep a short prefix for identification")
	}
}

func TestRenderTemplate(t *testing.T) {
	got := Render("<b>{title}</b> {size} {id}", TemplateData{
		Title: "A & B",
		Size:  "1 GB",
		ID:    42,
	})
	want := "<b>A &amp; B</b> 1 GB 42"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}

	// An empty template falls back to the default rather than producing an
	// empty caption.
	if out := Render("", TemplateData{Title: "x"}); out == "" {
		t.Error("Render with an empty template should fall back to the default")
	}
}
