package config

import (
	"strings"
	"testing"
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
