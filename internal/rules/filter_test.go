package rules

import (
	"strings"
	"testing"
)

func newDefaultFilter(t *testing.T) *Filter {
	t.Helper()
	f, err := NewFilter(defaultYAML, DefaultBlacklistForTest, DefaultGenreBlacklistForTest, DefaultGenreIDsForTest)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	return f
}

// These mirror the shipped defaults without importing the config package,
// which would pull in a dependency cycle for a test that only needs values.
var (
	DefaultBlacklistForTest      = []string{"欧美剧", "日韩剧", "综艺", "纪录片", "日番", "未分类"}
	DefaultGenreBlacklistForTest = []string{"真人秀"}
	DefaultGenreIDsForTest       = []int{99, 10764}
)

// The headline behaviour: with the default rules and the default blacklist,
// Chinese animation and Chinese TV pass while everything else is dropped. That
// is how "国漫 + 国产剧 only" is expressed.
func TestDefaultConfigurationKeepsOnlyChineseContent(t *testing.T) {
	f := newDefaultFilter(t)

	keep := []struct {
		name string
		md   Metadata
	}{
		{"国漫", Metadata{Genres: []string{"16"}, OriginCountries: []string{"CN"}}},
		{"国漫 台湾", Metadata{Genres: []string{"16"}, OriginCountries: []string{"TW"}}},
		{"国产剧", Metadata{Genres: []string{"18"}, OriginCountries: []string{"CN"}}},
	}
	for _, tc := range keep {
		if d := f.Evaluate("tv", tc.md, nil, nil); d.Blocked {
			t.Errorf("%s should be kept, blocked by %s", tc.name, d.Reason)
		}
	}

	drop := []struct {
		name string
		md   Metadata
	}{
		{"日番", Metadata{Genres: []string{"16"}, OriginCountries: []string{"JP"}}},
		{"欧美剧", Metadata{Genres: []string{"18"}, OriginCountries: []string{"US"}}},
		{"日韩剧", Metadata{Genres: []string{"18"}, OriginCountries: []string{"KR"}}},
	}
	for _, tc := range drop {
		if d := f.Evaluate("tv", tc.md, nil, nil); !d.Blocked {
			t.Errorf("%s should be dropped, got category %q", tc.name, d.Category)
		}
	}
}

// A release TMDB could not identify must be dropped rather than forwarded, or
// the channel slowly fills with whatever the tracker listed under 动漫.
func TestUnidentifiedReleaseIsDropped(t *testing.T) {
	f := newDefaultFilter(t)
	d := f.Evaluate("tv", Metadata{}, nil, nil)
	if !d.Blocked {
		t.Fatal("an unclassified release must be dropped when 未分类 is blacklisted")
	}
	if d.Category != Unclassified {
		t.Errorf("category = %q, want %q", d.Category, Unclassified)
	}
}

// With classification disabled there is no category to blacklist, so the
// genre lists are the only thing that can still block.
func TestGenresStillApplyWithoutClassification(t *testing.T) {
	f, err := NewFilter("", nil, []string{"真人秀"}, []int{99})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if d := f.Evaluate("tv", Metadata{}, []string{"99"}, nil); !d.Blocked {
		t.Error("an id in the genre blacklist must block")
	}
	if d := f.Evaluate("tv", Metadata{}, nil, []string{"真人秀"}); !d.Blocked {
		t.Error("a name in the genre blacklist must block")
	}
	if d := f.Evaluate("tv", Metadata{}, []string{"18"}, []string{"剧情"}); d.Blocked {
		t.Error("an unlisted genre must not block")
	}
	if d := f.Evaluate("tv", Metadata{}, nil, nil); d.Category != "" {
		t.Errorf("category = %q, want empty when classification is off", d.Category)
	}
}

// Genre ids arrive from TMDB as strings. A malformed one must be ignored
// rather than treated as a match, or a typo in the blacklist would silently
// drop every release that carries a genre.
func TestNonNumericGenreIDIsIgnored(t *testing.T) {
	f, err := NewFilter("", nil, nil, []int{99})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if d := f.Evaluate("tv", Metadata{}, []string{"oops", "", "99"}, nil); !d.Blocked {
		t.Error("a valid id beside junk ones must still block")
	}
	if d := f.Evaluate("tv", Metadata{}, []string{"oops"}, nil); d.Blocked {
		t.Error("an unparseable id must not block")
	}
}

// The category blacklist compares whole names, so a rule named 日番 must not be
// excluded by an entry for 日韩剧.
func TestCategoryBlacklistMatchesWholeNames(t *testing.T) {
	f, err := NewFilter(`
tv:
  日番:
    genre_ids: '16'
    origin_country: 'JP'
  日韩剧:
    origin_country: 'JP'
`, []string{"日韩剧"}, nil, nil)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	// A Japanese cartoon is 日番 by specificity, which is not blacklisted.
	if d := f.Evaluate("tv", Metadata{Genres: []string{"16"}, OriginCountries: []string{"JP"}}, nil, nil); d.Blocked {
		t.Errorf("日番 must not be caught by a 日韩剧 entry, got %s", d.Reason)
	}
	// A plain Japanese drama is 日韩剧, which is blacklisted.
	if d := f.Evaluate("tv", Metadata{OriginCountries: []string{"JP"}}, nil, nil); !d.Blocked {
		t.Error("日韩剧 should be blacklisted")
	}
}

// The genre name list is case-insensitive because names arrive localised and
// the operator may type them in any case.
func TestGenreNameBlacklistIgnoresCase(t *testing.T) {
	f, err := NewFilter("", nil, []string{"talk SHOW"}, nil)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if d := f.Evaluate("tv", Metadata{}, nil, []string{"Talk Show"}); !d.Blocked {
		t.Error("the name comparison must ignore case")
	}
}

// Every block has to say why, because the log is the only place an operator can
// find out that a release was dropped on purpose rather than failing.
func TestBlocksCarryAReason(t *testing.T) {
	f := newDefaultFilter(t)
	d := f.Evaluate("tv", Metadata{OriginCountries: []string{"US"}}, nil, nil)
	if !d.Blocked || d.Reason == "" {
		t.Fatalf("a block must carry a reason, got %+v", d)
	}
	if !strings.Contains(d.Reason, d.Category) {
		t.Errorf("reason %q should name the category %q", d.Reason, d.Category)
	}
}

func TestNilFilterIsSafe(t *testing.T) {
	var f *Filter
	if d := f.Evaluate("tv", Metadata{}, nil, nil); d.Blocked {
		t.Error("a nil filter must not block")
	}
	if f.Enabled() {
		t.Error("a nil filter is not enabled")
	}
	if got := f.Classify("tv", Metadata{}); got != "" {
		t.Errorf("Classify = %q, want empty", got)
	}
}

func TestEnabledReflectsConfiguration(t *testing.T) {
	off, err := NewFilter("", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if off.Enabled() {
		t.Error("an empty filter should report disabled")
	}
	on := newDefaultFilter(t)
	if !on.Enabled() {
		t.Error("the default filter should report enabled")
	}
}

func TestNewFilterRejectsBadRules(t *testing.T) {
	if _, err := NewFilter("movie: [oops", nil, nil, nil); err == nil {
		t.Error("a malformed rule set must be reported")
	}
}
