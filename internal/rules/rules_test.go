package rules

import "testing"

// The default rules, kept here verbatim so the tests fail if the shipped
// defaults stop classifying the way they are documented to.
const defaultYAML = `
movie:
  动画电影:
    genre_ids: '16'
  华语电影:
    original_language: 'zh,cn,bo,za'
  日韩电影:
    original_language: 'ja,ko'
  欧美电影:
    original_language: 'en,fr,de,es,it,pt,nl,ru'
  其他电影:
tv:
  国漫:
    genre_ids: '16'
    origin_country: 'CN,TW,HK'
  日番:
    genre_ids: '16'
    origin_country: 'JP'
  纪录片:
    genre_ids: '99'
  儿童:
    genre_ids: '10762'
  综艺:
    genre_ids: '10764,10767'
  国产剧:
    origin_country: 'CN,TW,HK'
  欧美剧:
    origin_country: 'US,FR,GB,DE,ES,IT,NL,PT,RU,UK'
  日韩剧:
    origin_country: 'JP,KP,KR,TH,IN,SG'
  未分类:
`

func parseDefault(t *testing.T) *Classifier {
	t.Helper()
	c, err := Parse([]byte(defaultYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

// 国漫 sets two conditions while 国产剧 sets one, so a Chinese cartoon has to
// resolve to 国漫. If specificity were ignored the result would depend on
// alphabetical or map order and Chinese animation would be filed as a drama.
func TestChineseAnimationIsNotFiledAsDrama(t *testing.T) {
	c := parseDefault(t)
	got := c.Classify("tv", Metadata{
		Genres:          []string{"16"},
		OriginCountries: []string{"CN"},
	})
	if got != "国漫" {
		t.Errorf("Classify = %q, want 国漫", got)
	}
}

// The ordering must not depend on Go's map iteration order, which is
// deliberately randomised.
func TestClassificationIsStableAcrossRepeatedCalls(t *testing.T) {
	c := parseDefault(t)
	md := Metadata{Genres: []string{"16"}, OriginCountries: []string{"TW"}}
	for i := 0; i < 50; i++ {
		if got := c.Classify("tv", md); got != "国漫" {
			t.Fatalf("call %d: Classify = %q, want 国漫", i, got)
		}
	}
}

func TestTVRegions(t *testing.T) {
	c := parseDefault(t)
	cases := []struct {
		name string
		md   Metadata
		want string
	}{
		{"中国电视剧", Metadata{OriginCountries: []string{"CN"}}, "国产剧"},
		{"香港剧", Metadata{OriginCountries: []string{"HK"}}, "国产剧"},
		{"日本番剧", Metadata{Genres: []string{"16"}, OriginCountries: []string{"JP"}}, "日番"},
		{"日剧", Metadata{OriginCountries: []string{"JP"}}, "日韩剧"},
		{"美剧", Metadata{OriginCountries: []string{"US"}}, "欧美剧"},
		{"英剧", Metadata{OriginCountries: []string{"GB"}}, "欧美剧"},
		{"纪录片", Metadata{Genres: []string{"99"}, OriginCountries: []string{"US"}}, "纪录片"},
		{"综艺", Metadata{Genres: []string{"10764"}, OriginCountries: []string{"CN"}}, "综艺"},
		{"儿童", Metadata{Genres: []string{"10762"}, OriginCountries: []string{"US"}}, "儿童"},
	}
	for _, tc := range cases {
		if got := c.Classify("tv", tc.md); got != tc.want {
			t.Errorf("%s: Classify = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A movie has no origin_country, so a rule written against it must still work
// by falling back to production_countries.
func TestMovieUsesProductionCountriesForOriginRules(t *testing.T) {
	c, err := Parse([]byte(`
movie:
  华语电影:
    origin_country: 'CN,TW,HK'
  其他电影:
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := c.Classify("movie", Metadata{ProductionCountries: []string{"CN", "HK"}})
	if got != "华语电影" {
		t.Errorf("Classify = %q, want 华语电影", got)
	}
}

func TestMovieOriginalLanguage(t *testing.T) {
	c := parseDefault(t)
	cases := map[string]string{
		"zh": "华语电影", "cn": "华语电影", "ja": "日韩电影", "ko": "日韩电影",
		"en": "欧美电影", "ru": "欧美电影",
	}
	for lang, want := range cases {
		if got := c.Classify("movie", Metadata{OriginalLanguage: lang}); got != want {
			t.Errorf("language %q: Classify = %q, want %q", lang, got, want)
		}
	}
}

// A movie with no matching rule and no conditions falls through to the
// catch-all, so nothing is silently dropped.
func TestCatchAllHandlesTheRemainder(t *testing.T) {
	c := parseDefault(t)
	if got := c.Classify("movie", Metadata{OriginalLanguage: "sv"}); got != "其他电影" {
		t.Errorf("Classify = %q, want 其他电影", got)
	}
	if got := c.Classify("tv", Metadata{OriginCountries: []string{"BR"}}); got != "未分类" {
		t.Errorf("Classify = %q, want 未分类", got)
	}
}

// The catch-all must not win over a rule that actually matches, even though it
// is reached last.
func TestCatchAllDoesNotShadowAMatch(t *testing.T) {
	c := parseDefault(t)
	if got := c.Classify("tv", Metadata{OriginCountries: []string{"US"}}); got != "欧美剧" {
		t.Errorf("Classify = %q, want 欧美剧 rather than the catch-all", got)
	}
}

// "!value" excludes, and several comma separated values are alternatives.
func TestNegationAndAlternatives(t *testing.T) {
	c, err := Parse([]byte(`
tv:
  非日番动画:
    genre_ids: '16'
    origin_country: '!JP'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Classify("tv", Metadata{Genres: []string{"16"}, OriginCountries: []string{"JP"}}); got != "" {
		t.Errorf("a Japanese cartoon must be excluded, got %q", got)
	}
	if got := c.Classify("tv", Metadata{Genres: []string{"16"}, OriginCountries: []string{"US"}}); got != "非日番动画" {
		t.Errorf("a non-Japanese cartoon should match, got %q", got)
	}
}

// An exclusion must beat a positive entry in the same field, otherwise "en,!US"
// would match a US release through its "en" entry.
func TestExclusionBeatsInclusion(t *testing.T) {
	c, err := Parse([]byte(`
movie:
  英语但非美国:
    origin_country: 'US,GB,!US'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Classify("movie", Metadata{ProductionCountries: []string{"US"}}); got != "" {
		t.Errorf("the exclusion must win, got %q", got)
	}
	if got := c.Classify("movie", Metadata{ProductionCountries: []string{"GB"}}); got != "英语但非美国" {
		t.Errorf("a non-excluded value should match, got %q", got)
	}
}

func TestYearRules(t *testing.T) {
	c, err := Parse([]byte(`
movie:
  近年:
    release_year: '2020-2025'
  经典:
    release_year: '1999'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cases := []struct {
		year int
		want string
	}{
		{2020, "近年"}, {2025, "近年"}, {2023, "近年"},
		{2019, ""}, {2026, ""}, {1999, "经典"}, {2000, ""},
	}
	for _, tc := range cases {
		if got := c.Classify("movie", Metadata{Year: tc.year}); got != tc.want {
			t.Errorf("year %d: Classify = %q, want %q", tc.year, got, tc.want)
		}
	}
}

// An unknown year must not match a year rule: treating 0 as "any year" would
// silently file every undated release into the first dated rule.
func TestUnknownYearDoesNotMatchAYearRule(t *testing.T) {
	c, err := Parse([]byte(`
movie:
  近年:
    release_year: '2020-2025'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Classify("movie", Metadata{Year: 0}); got != "" {
		t.Errorf("Classify = %q, want no match for an unknown year", got)
	}
}

// A missing field on the TMDB side must never satisfy a rule that constrains
// it, or every unenriched release would land in the first category.
func TestMissingMetadataDoesNotMatchConstrainedFields(t *testing.T) {
	c := parseDefault(t)
	if got := c.Classify("tv", Metadata{}); got != "未分类" {
		t.Errorf("an empty match should reach the catch-all, got %q", got)
	}
}

func TestNilClassifierAndUnknownKind(t *testing.T) {
	var c *Classifier
	if got := c.Classify("tv", Metadata{}); got != "" {
		t.Errorf("a nil classifier should return empty, got %q", got)
	}
	d := parseDefault(t)
	if got := d.Classify("person", Metadata{}); got != "" {
		t.Errorf("an unknown kind should return empty, got %q", got)
	}
}

func TestParseRejectsMalformedYAML(t *testing.T) {
	if _, err := Parse([]byte("movie: [this is not a mapping")); err == nil {
		t.Error("malformed YAML must be reported rather than ignored")
	}
}

// Genre ids arrive from TMDB as numbers and are compared as strings, so a rule
// written with or without quotes has to behave the same.
func TestGenreIDMatchingIsStringBased(t *testing.T) {
	c, err := Parse([]byte(`
tv:
  动画:
    genre_ids: 16, 10759
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Classify("tv", Metadata{Genres: []string{"10759"}}); got != "动画" {
		t.Errorf("Classify = %q, want 动画", got)
	}
}

// Two rules of equal specificity that constrain different dimensions can both
// match, for example a US documentary against 纪录片 (genre 99) and 欧美剧
// (origin US). The file order must decide, because only the operator knows
// which grouping they want first; deciding by category name would make the
// result depend on Chinese byte order.
func TestEqualSpecificityTiesFollowFileOrder(t *testing.T) {
	doc := `
tv:
  纪录片:
    genre_ids: '99'
  欧美剧:
    origin_country: 'US'
`
	c, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	md := Metadata{Genres: []string{"99"}, OriginCountries: []string{"US"}}
	if got := c.Classify("tv", md); got != "纪录片" {
		t.Errorf("Classify = %q, want 纪录片, the first rule written", got)
	}

	// Reversing the file must reverse the answer, which is what proves the
	// order is being read rather than guessed.
	reversed := `
tv:
  欧美剧:
    origin_country: 'US'
  纪录片:
    genre_ids: '99'
`
	c2, err := Parse([]byte(reversed))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c2.Classify("tv", md); got != "欧美剧" {
		t.Errorf("Classify = %q, want 欧美剧, the first rule written", got)
	}
}

// Specificity must outrank file order, otherwise a catch-all-ish rule written
// first would swallow the more precise ones after it.
func TestSpecificityOutranksFileOrder(t *testing.T) {
	c, err := Parse([]byte(`
tv:
  国产剧:
    origin_country: 'CN,TW,HK'
  国漫:
    genre_ids: '16'
    origin_country: 'CN,TW,HK'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	md := Metadata{Genres: []string{"16"}, OriginCountries: []string{"CN"}}
	if got := c.Classify("tv", md); got != "国漫" {
		t.Errorf("Classify = %q, want 国漫 despite being written second", got)
	}
}

// Unknown top-level sections must be ignored so one file can be shared with
// the organising tool that has its own keys.
func TestUnknownSectionsAreIgnored(t *testing.T) {
	c, err := Parse([]byte(`
something_else:
  whatever: true
movie:
  动画电影:
    genre_ids: '16'
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Classify("movie", Metadata{Genres: []string{"16"}}); got != "动画电影" {
		t.Errorf("Classify = %q, want 动画电影", got)
	}
}

func TestParseRejectsNonMappingSections(t *testing.T) {
	if _, err := Parse([]byte("tv: [a, b]\n")); err == nil {
		t.Error("a non-mapping section must be reported")
	}
}
