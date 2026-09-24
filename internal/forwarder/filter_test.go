package forwarder

import (
	"testing"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/scrape"
)

func item(title string, sizeMB float64) scrape.Item {
	return scrape.Item{ID: 1, Title: title, SizeMB: sizeMB}
}

func TestFilterIncludeExclude(t *testing.T) {
	f, err := newFilter(config.Settings{
		Include: []string{`1080p`, `2160p`},
		Exclude: []string{`cam\b`},
	})
	if err != nil {
		t.Fatalf("newFilter: %v", err)
	}
	cases := []struct {
		title string
		want  bool
	}{
		{"Movie 2026 1080p WEB-DL", true},
		{"Movie 2026 2160p BluRay", true},
		{"Movie 2026 720p WEB-DL", false},
		{"Movie 2026 1080p CAM", false},
		{"concert CAM rip", false},
	}
	for _, tc := range cases {
		if got := f.allow(item(tc.title, 0)); got != tc.want {
			t.Errorf("allow(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}

// TestFilterIsCaseInsensitive guards the (?i) prefix applied to every pattern.
func TestFilterIsCaseInsensitive(t *testing.T) {
	f, err := newFilter(config.Settings{Include: []string{"webrip"}})
	if err != nil {
		t.Fatalf("newFilter: %v", err)
	}
	if !f.allow(item("Movie 2026 WEBRip x264", 0)) {
		t.Error("patterns should match case-insensitively")
	}
}

func TestFilterSizeBounds(t *testing.T) {
	f, err := newFilter(config.Settings{MinSizeMB: 700, MaxSizeMB: 2000})
	if err != nil {
		t.Fatalf("newFilter: %v", err)
	}
	if f.allow(item("too small", 300)) {
		t.Error("a title below the minimum size should be rejected")
	}
	if f.allow(item("too big", 5000)) {
		t.Error("a title above the maximum size should be rejected")
	}
	if !f.allow(item("just right", 1024)) {
		t.Error("a title within the size range should pass")
	}
	// Unknown sizes must not be filtered out, otherwise rows missing a size
	// would silently disappear.
	if !f.allow(item("unknown size", 0)) {
		t.Error("a title with an unknown size should pass when bounds are set")
	}
}

func TestFilterRejectsInvalidPattern(t *testing.T) {
	if _, err := newFilter(config.Settings{Include: []string{"[unclosed"}}); err == nil {
		t.Error("expected an error for an invalid regular expression")
	}
}

func TestParseThreadID(t *testing.T) {
	cases := []struct {
		in   string
		want int
		bad  bool
	}{
		{"", 0, false},
		{"42", 42, false},
		{"  7  ", 7, false},
		{"topic99", 99, false},
		{"https://t.me/c/1234567890/42", 42, false},
		{"not-a-number", 0, true},
	}
	for _, tc := range cases {
		got, err := parseThreadID(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("parseThreadID(%q) should have failed", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseThreadID(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseThreadID(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
