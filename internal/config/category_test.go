package config

import (
	"strings"
	"testing"

	"github.com/haoyu010/ext.to/internal/rules"
)

// The shipped defaults are the answer to "keep 国漫 and 国产剧 only", so they
// must survive validation untouched.
func TestDefaultCategoryConfigurationValidates(t *testing.T) {
	s := Default()
	if err := s.Validate(); err != nil {
		t.Fatalf("the shipped defaults must validate: %v", err)
	}
	if strings.TrimSpace(s.CategoryRules) == "" {
		t.Error("classification should be on by default")
	}
	for _, want := range []string{"欧美剧", "日韩剧", "日番", "未分类"} {
		found := false
		for _, got := range s.CategoryBlacklist {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the default blacklist should exclude %s: %v", want, s.CategoryBlacklist)
		}
	}
}

// A blacklist entry naming a category no rule defines can never fire. The
// usual cause is a typo, and silently accepting it would leave the operator
// believing a category is excluded when it is not.
func TestValidateRejectsUnknownBlacklistName(t *testing.T) {
	s := Default()
	s.CategoryRules = "tv:\n  国漫:\n    genre_ids: '16'\n    origin_country: 'CN'\n"
	s.CategoryBlacklist = []string{"国漫", "日韩剧"}
	err := s.Validate()
	if err == nil {
		t.Fatal("an unknown blacklist name must be reported")
	}
	if !strings.Contains(err.Error(), "日韩剧") {
		t.Errorf("the error should name the offending entry, got %v", err)
	}
	if strings.Contains(err.Error(), "国漫") {
		t.Errorf("the valid entry must not be reported: %v", err)
	}
}

// 未分类 is produced for any release that is matched but named by no rule, so
// it is a legitimate entry even when no catch-all rule defines it. Rejecting it
// would make the most useful default entry unsavable.
func TestValidateAcceptsUnclassifiedWithoutACatchAll(t *testing.T) {
	s := Default()
	s.CategoryRules = "tv:\n  国漫:\n    genre_ids: '16'\n    origin_country: 'CN'\n"
	s.CategoryBlacklist = []string{"未分类"}
	if err := s.Validate(); err != nil {
		t.Errorf("未分类 must be accepted without a catch-all rule: %v", err)
	}
}

// A blacklist with no rules at all would stop nothing in a way that looks like
// it should work, so it is rejected rather than obeyed.
func TestValidateRejectsBlacklistWithoutRules(t *testing.T) {
	s := Default()
	s.CategoryRules = ""
	s.CategoryBlacklist = []string{"国漫"}
	if err := s.Validate(); err == nil {
		t.Fatal("a blacklist with no rules must be reported")
	}
}

// A non-positive genre id can never match a TMDB genre, so it is caught on
// save instead of quietly doing nothing.
func TestValidateRejectsNonPositiveGenreID(t *testing.T) {
	s := Default()
	s.GenreIDBlacklist = []int{0}
	if err := s.Validate(); err == nil {
		t.Fatal("a zero genre id must be reported")
	}
}

// The default blacklist has to name every category the default rules can
// produce except the two the channel actually wants. A category left out is
// forwarded, so an omission is a silent leak rather than a visible error: a
// Bilibili release with no recognised country lands in 儿童, and no movie
// category is Chinese, yet both would pass straight through.
func TestDefaultBlacklistCoversEveryUnwantedCategory(t *testing.T) {
	s := Default()
	c, err := rules.Parse([]byte(s.CategoryRules))
	if err != nil {
		t.Fatalf("the shipped rules must parse: %v", err)
	}
	kept := map[string]bool{"国漫": true, "国产剧": true}
	blocked := map[string]bool{}
	for _, n := range s.CategoryBlacklist {
		blocked[n] = true
	}
	for _, name := range c.Names() {
		if kept[name] {
			continue
		}
		if !blocked[name] {
			t.Errorf("rule %q can be produced but is not in the default blacklist, "+
				"so the default configuration would forward it", name)
		}
	}
}

// The two categories the channel is for must not be excluded by the shipped
// defaults, or the default configuration would forward nothing at all.
func TestDefaultBlacklistKeepsChineseCategories(t *testing.T) {
	s := Default()
	for _, name := range []string{"国漫", "国产剧"} {
		for _, b := range s.CategoryBlacklist {
			if b == name {
				t.Errorf("%s must not be blacklisted: %v", name, s.CategoryBlacklist)
			}
		}
	}
}

// The default tracker categories have to be able to deliver what the default
// rules keep. The tracker files Chinese animation under 动漫 rather than under
// 剧集, so watching 剧集 alone would collect nothing the rules want and make a
// working filter look broken.
func TestDefaultCategoriesCanDeliverChineseAnimation(t *testing.T) {
	s := Default()
	watched := map[int]bool{}
	for _, c := range s.Categories {
		watched[c] = true
	}
	if !watched[CatAnime] && !watched[CatAll] {
		t.Errorf("categories %v cannot deliver 国漫, which the rules keep", s.Categories)
	}
}
