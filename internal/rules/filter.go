package rules

import (
	"strconv"
	"strings"
)

// Unclassified is the category reported when no rule produced a name.
const Unclassified = "未分类"

// Decision is the outcome of classifying and filtering one TMDB match.
type Decision struct {
	// Category is the classified name, always set once Evaluate has run.
	Category string
	// Blocked reports whether a blacklist rejected the release.
	Blocked bool
	// Reason explains a block in the operator's words, for the log.
	Reason string
}

// Filter applies a rule set plus the three blacklists.
//
// The blacklists exist because the tracker's own category is far too coarse to
// answer the question that matters here: its 动漫 category holds Chinese,
// Japanese and Western animation alike, so "Chinese animation only" cannot be
// expressed as a tracker category at all. It can only be decided from the TMDB
// match, after resolving which animation it actually is.
type Filter struct {
	classifier *Classifier
	categories map[string]bool
	genres     map[string]bool
	genreIDs   map[int]bool
}

// NewFilter compiles a classifier and the blacklists. ruleYAML may be empty,
// which disables classification; the name blacklists then have nothing to match
// and only the genre id list still applies.
func NewFilter(ruleYAML string, categories, genres []string, genreIDs []int) (*Filter, error) {
	f := &Filter{
		categories: map[string]bool{},
		genres:     map[string]bool{},
		genreIDs:   map[int]bool{},
	}
	if strings.TrimSpace(ruleYAML) != "" {
		c, err := Parse([]byte(ruleYAML))
		if err != nil {
			return nil, err
		}
		f.classifier = c
	}
	for _, c := range categories {
		if c = strings.TrimSpace(c); c != "" {
			f.categories[c] = true
		}
	}
	for _, g := range genres {
		if g = strings.TrimSpace(g); g != "" {
			f.genres[strings.ToLower(g)] = true
		}
	}
	for _, id := range genreIDs {
		f.genreIDs[id] = true
	}
	return f, nil
}

// Classify names the category for a match. An empty result means either that
// classification is off or that no rule produced a name.
func (f *Filter) Classify(kind string, md Metadata) string {
	if f == nil || f.classifier == nil {
		return ""
	}
	return f.classifier.Classify(kind, md)
}

// Evaluate classifies a match and applies every blacklist.
//
// A release no rule could name is evaluated as 未分类 rather than skipped:
// when classification is configured, an unidentifiable release is exactly what
// a tightly scoped channel does not want, so it has to face the blacklist like
// any other. When classification is off there is no category and only the genre
// blacklists apply.
func (f *Filter) Evaluate(kind string, md Metadata, genreIDs []string, genreNames []string) Decision {
	if f == nil {
		return Decision{}
	}

	category := ""
	if f.classifier != nil {
		category = f.classifier.Classify(kind, md)
		if category == "" {
			category = Unclassified
		}
		if f.categories[category] {
			return Decision{
				Category: category,
				Blocked:  true,
				Reason:   "分类「" + category + "」在黑名单中",
			}
		}
	}

	for _, id := range genreIDs {
		n, err := strconv.Atoi(strings.TrimSpace(id))
		if err != nil {
			// A non numeric id cannot be blacklisted, and inventing a
			// failure here would drop releases for a configuration typo.
			continue
		}
		if f.genreIDs[n] {
			return Decision{
				Category: category,
				Blocked:  true,
				Reason:   "Genre ID " + strconv.Itoa(n) + " 在黑名单中",
			}
		}
	}
	for _, name := range genreNames {
		if f.genres[strings.ToLower(strings.TrimSpace(name))] {
			return Decision{
				Category: category,
				Blocked:  true,
				Reason:   "类型「" + name + "」在黑名单中",
			}
		}
	}
	return Decision{Category: category}
}

// Enabled reports whether any classification or blacklist is configured.
func (f *Filter) Enabled() bool {
	if f == nil {
		return false
	}
	return f.classifier != nil || len(f.categories) > 0 || len(f.genres) > 0 || len(f.genreIDs) > 0
}
