// Package rules classifies a TMDB match into a named category and applies the
// operator's blacklists.
//
// The rule syntax follows the same shape as the tracking tools that already
// organise these libraries, so an existing category file can be reused:
//
//	genre_ids:            16          TMDB genre ids, comma separated
//	original_language:    zh,cn       TMDB original_language values
//	origin_country:       CN,TW,HK    TV origin_country values
//	production_countries: CN          movie production_countries iso codes
//	release_year:         2020-2025   inclusive range, or a single year
//
// Conditions within one rule are ANDed; several comma separated values for one
// field are ORed; a leading "!" excludes a value. A rule with no conditions is
// a catch-all, used only when nothing else matches.
//
// Rules are tried most specific first, where specificity is the number of
// conditions set, and rules that set the same number of conditions keep the
// order they were written in. Both halves matter:
//
//   - Specificity first means 国漫 (animation AND Chinese origin) beats 国产剧
//     (Chinese origin alone) no matter how the file is written, so a Chinese
//     cartoon is never filed as a drama.
//   - File order then breaks ties between rules that constrain different
//     dimensions, such as 纪录片 (genre 99) versus 欧美剧 (origin US): a US
//     documentary satisfies both, and only the operator knows which they want
//     first. Sorting ties by name instead would decide it by byte order.
package rules

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rule is one named category and the conditions that select it.
type Rule struct {
	GenreIDs            string `yaml:"genre_ids,omitempty"`
	OriginalLanguage    string `yaml:"original_language,omitempty"`
	OriginCountry       string `yaml:"origin_country,omitempty"`
	ProductionCountries string `yaml:"production_countries,omitempty"`
	ReleaseYear         string `yaml:"release_year,omitempty"`
}

// NamedRule is one category and its conditions.
type NamedRule struct {
	Name string
	Rule Rule
}

// Config holds the rules for movies and for TV, in the order they were written.
// The order is part of the configuration, so this is a slice rather than a map:
// a Go map would discard it and force a tie-break that the operator did not ask
// for.
type Config struct {
	Movie []NamedRule
	TV    []NamedRule
}

// Metadata is the TMDB information a rule is matched against.
type Metadata struct {
	// Genres are TMDB genre ids as strings, matching the rule syntax.
	Genres []string
	// OriginalLanguage is TMDB's original_language, for example "zh" or "en".
	OriginalLanguage string
	// OriginCountries is a TV show's origin_country. For a movie it is filled
	// from production_countries so one rule field can serve both.
	OriginCountries []string
	// ProductionCountries is a movie's production_countries.
	ProductionCountries []string
	// Year is the release or first-air year, 0 when unknown.
	Year int
}

// Classifier resolves categories from a Config.
type Classifier struct {
	movie []namedRule
	tv    []namedRule
}

type namedRule struct {
	name string
	rule Rule
}

// New compiles a classifier, ordering rules most specific first.
func New(cfg Config) *Classifier {
	return &Classifier{
		movie: order(cfg.Movie),
		tv:    order(cfg.TV),
	}
}

// Parse compiles a classifier from YAML.
//
// The document is walked as a yaml.Node rather than unmarshalled into a map,
// because a map loses the order the categories were written in and that order
// is what decides ties between equally specific rules.
func Parse(data []byte) (*Classifier, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("解析分类规则失败：%w", err)
	}
	cfg, err := configFromNode(&doc)
	if err != nil {
		return nil, err
	}
	return New(*cfg), nil
}

func configFromNode(doc *yaml.Node) (*Config, error) {
	// A document node wraps the real mapping; an empty file yields nothing.
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return &Config{}, nil
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("分类规则的顶层必须是 movie / tv 映射")
	}

	cfg := &Config{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch strings.TrimSpace(key.Value) {
		case "movie":
			list, err := namedRulesFromNode("movie", value)
			if err != nil {
				return nil, err
			}
			cfg.Movie = list
		case "tv":
			list, err := namedRulesFromNode("tv", value)
			if err != nil {
				return nil, err
			}
			cfg.TV = list
		}
		// Unknown top-level keys are ignored so a file shared with the
		// organising tool can carry its own sections without breaking this one.
		// They must be skipped before any decoding, because their contents need
		// not look anything like a rule and would otherwise fail to parse.
	}
	return cfg, nil
}

func namedRulesFromNode(section string, node *yaml.Node) ([]NamedRule, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s 必须是「分类名: 规则」的映射", section)
	}
	out := make([]NamedRule, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := strings.TrimSpace(node.Content[i].Value)
		if name == "" {
			return nil, fmt.Errorf("%s 里有一个空的分类名", section)
		}
		var rule Rule
		if err := node.Content[i+1].Decode(&rule); err != nil {
			return nil, fmt.Errorf("%s 的「%s」规则无法解析：%w", section, name, err)
		}
		out = append(out, NamedRule{Name: name, Rule: rule})
	}
	return out, nil
}

// order sorts rules most specific first, keeping the configured order for ties.
// sort.SliceStable is what preserves the tie order.
func order(list []NamedRule) []namedRule {
	out := make([]namedRule, 0, len(list))
	for _, nr := range list {
		out = append(out, namedRule{name: nr.Name, rule: nr.Rule})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return specificity(out[i].rule) > specificity(out[j].rule)
	})
	return out
}

func specificity(r Rule) int {
	n := 0
	for _, v := range []string{r.GenreIDs, r.OriginalLanguage, r.OriginCountry, r.ProductionCountries, r.ReleaseYear} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	return n
}

// Classify returns the category for a TMDB match. kind is "movie" or "tv".
// The empty string means no rule matched and no catch-all was configured.
func (c *Classifier) Classify(kind string, md Metadata) string {
	if c == nil {
		return ""
	}
	var list []namedRule
	switch kind {
	case "movie":
		list = c.movie
	case "tv":
		list = c.tv
	default:
		return ""
	}

	fallback := ""
	for _, item := range list {
		if specificity(item.rule) == 0 {
			// A catch-all is remembered, not returned, so it cannot shadow a
			// later rule that actually matches.
			if fallback == "" {
				fallback = item.name
			}
			continue
		}
		if match(item.rule, md) {
			return item.name
		}
	}
	return fallback
}

// Names returns every category name the rule set defines, across both
// sections. It exists so a caller can check that a blacklist entry actually
// refers to a rule: an entry naming nothing is dead configuration, and the
// usual cause is a typo or a name left behind after the rules were edited.
func (c *Classifier) Names() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.movie)+len(c.tv))
	for _, item := range c.movie {
		out = append(out, item.name)
	}
	for _, item := range c.tv {
		out = append(out, item.name)
	}
	return out
}

func match(r Rule, md Metadata) bool {
	if r.GenreIDs != "" && !matchAny(r.GenreIDs, md.Genres) {
		return false
	}
	if r.OriginalLanguage != "" && !matchOne(r.OriginalLanguage, md.OriginalLanguage) {
		return false
	}
	if r.OriginCountry != "" {
		// A movie has no origin_country, so production_countries stands in.
		countries := md.OriginCountries
		if len(countries) == 0 {
			countries = md.ProductionCountries
		}
		if !matchAny(r.OriginCountry, countries) {
			return false
		}
	}
	if r.ProductionCountries != "" && !matchAny(r.ProductionCountries, md.ProductionCountries) {
		return false
	}
	if r.ReleaseYear != "" && !matchYear(r.ReleaseYear, md.Year) {
		return false
	}
	return true
}

// matchAny reports whether the rule list selects any of the actual values.
// A "!" prefixed entry excludes, and an exclusion always wins: if the actual
// values contain an excluded value the whole field fails, regardless of what
// else was listed.
func matchAny(ruleValues string, actual []string) bool {
	parts := splitList(ruleValues)
	var positives []string
	for _, p := range parts {
		if neg, ok := strings.CutPrefix(p, "!"); ok {
			for _, a := range actual {
				if strings.EqualFold(strings.TrimSpace(neg), a) {
					return false
				}
			}
			continue
		}
		positives = append(positives, p)
	}
	// A rule that only excludes matches everything it did not exclude.
	if len(positives) == 0 {
		return true
	}
	for _, p := range positives {
		for _, a := range actual {
			if strings.EqualFold(p, a) {
				return true
			}
		}
	}
	return false
}

func matchOne(ruleValues, actual string) bool {
	if strings.TrimSpace(actual) == "" {
		return false
	}
	for _, p := range splitList(ruleValues) {
		if strings.EqualFold(p, actual) {
			return true
		}
	}
	return false
}

func matchYear(rule string, year int) bool {
	rule = strings.TrimSpace(rule)
	if year == 0 {
		return false
	}
	if lo, hi, ok := strings.Cut(rule, "-"); ok {
		start, err1 := strconv.Atoi(strings.TrimSpace(lo))
		end, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil {
			return false
		}
		return year >= start && year <= end
	}
	exact, err := strconv.Atoi(rule)
	if err != nil {
		return false
	}
	return year == exact
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
