package scrape

import (
	"os"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// TestParseListAgainstCapturedPage guards the listing parser against the real
// markup shape, which places href before class on the title anchor.
func TestParseListAgainstCapturedPage(t *testing.T) {
	items, err := ParseList(readFixture(t, "browse.html"))
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("ParseList returned no items; the row selector or markup shape changed")
	}
	for i, it := range items {
		if it.ID <= 0 {
			t.Errorf("item %d: missing id (%+v)", i, it)
		}
		if strings.TrimSpace(it.Title) == "" {
			t.Errorf("item %d: missing title", i)
		}
		if it.URL == "" || !strings.HasPrefix(it.URL, "http") {
			t.Errorf("item %d: bad url %q", i, it.URL)
		}
		if it.Size == "" {
			t.Errorf("item %d (%s): missing size", i, it.Title)
		}
		if it.SizeMB <= 0 {
			t.Errorf("item %d (%s): size %q did not convert to MB", i, it.Title, it.Size)
		}
		if it.Age == "" {
			t.Errorf("item %d (%s): missing age", i, it.Title)
		}
	}
}

// TestParseListFields checks specific field values against the first fixture
// row, which is a known Netflix WEB-DL entry.
func TestParseListFields(t *testing.T) {
	items, err := ParseList(readFixture(t, "browse.html"))
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	first := items[0]
	t.Logf("first item: %+v", first)
	if first.Category == "" {
		t.Error("category was not extracted from the posted-by breadcrumb")
	}
	if first.Uploader == "" {
		t.Error("uploader was not extracted from the posted-by breadcrumb")
	}
	if !strings.Contains(first.URL, "/") {
		t.Errorf("url should be absolute, got %q", first.URL)
	}
	if !strings.HasSuffix(strings.TrimRight(first.URL, "/"), itoa(first.ID)) {
		t.Errorf("url %q does not end with id %d", first.URL, first.ID)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var out []byte
	for v > 0 {
		out = append([]byte{byte('0' + v%10)}, out...)
		v /= 10
	}
	return string(out)
}

func TestParseTokens(t *testing.T) {
	body := readFixture(t, "detail_movie.html")
	token, csrf := parseTokens(body)
	if token == "" {
		t.Error("page token not found in detail fixture")
	}
	if csrf == "" {
		t.Error("csrf token not found in detail fixture")
	}
	if len(csrf) < 16 {
		t.Errorf("csrf token looks wrong: %q", csrf)
	}
}

func TestParsePosterURLPrefersOriginal(t *testing.T) {
	body := readFixture(t, "detail_movie.html")
	got := parsePosterURL(body)
	if got == "" {
		t.Fatal("no poster found in detail fixture")
	}
	if strings.Contains(got, "/resize_cache/") {
		t.Errorf("poster should be the full-size asset, got %q", got)
	}
	if strings.Count(got, "//") != 1 {
		t.Errorf("poster URL has a malformed path: %q", got)
	}
}

// TestIsChallenge ensures a real page is not mistaken for an interstitial.
func TestIsChallenge(t *testing.T) {
	if IsChallenge(readFixture(t, "browse.html")) {
		t.Error("real listing page was misdetected as a Cloudflare challenge")
	}
	challenge := []byte(`<html><head><title>Just a moment...</title></head>` +
		`<body><span id="challenge-error-text">Enable JavaScript</span></body></html>`)
	if !IsChallenge(challenge) {
		t.Error("challenge page was not detected")
	}
}

func TestIDFromSlug(t *testing.T) {
	cases := []struct {
		slug string
		id   int
		ok   bool
	}{
		{"ring-ring-2019-1080p-webrip-22395007", 22395007, true},
		{"a-knight-039-s-war-m147425", 0, false},
		{"no-id-here", 0, false},
	}
	for _, tc := range cases {
		got, ok := idFromSlug(tc.slug)
		if ok != tc.ok || got != tc.id {
			t.Errorf("idFromSlug(%q) = (%d, %v), want (%d, %v)", tc.slug, got, ok, tc.id, tc.ok)
		}
	}
}
