// Package tmdb resolves torrent releases against The Movie Database.
//
// A release is matched in one of two ways:
//
//  1. With an external id. ext.to detail pages expose an IMDb id, which
//     /find/{id} converts into a TMDB movie or TV id.
//  2. By title alone. The release name is reduced to its first line, and only
//     an exact normalised title match is accepted.
//
// Descriptions, share text, subtitle notes, sizes, magnet links and tags are
// never used for matching; they add noise rather than signal.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/haoyu010/ext.to/internal/media"
)

// BaseURL is the API origin, overridable so tests can serve fixtures.
var BaseURL = "https://api.themoviedb.org/3"

// ImageBase is the poster CDN prefix.
var ImageBase = "https://image.tmdb.org/t/p"

// ErrNotConfigured is returned when no API key has been supplied.
var ErrNotConfigured = errors.New("tmdb api key is not configured")

// ErrNoMatch indicates the release could not be resolved to a TMDB entry.
var ErrNoMatch = errors.New("no tmdb match")

// ErrUnauthorized means TMDB accepted the request but not the credential. It
// is a distinct value so the dashboard can name the fix in its own words
// rather than showing the raw upstream English.
var ErrUnauthorized = errors.New("tmdb rejected the api key")

// ErrRateLimited means the account is being throttled, which a slower scan
// resolves.
var ErrRateLimited = errors.New("tmdb rate limit reached")

// Entry is a resolved TMDB record.
type Entry struct {
	ID            int     `json:"id"`
	Type          string  `json:"type"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          int     `json:"year"`
	Rating        float64 `json:"rating"`
	VoteCount     int     `json:"vote_count"`
	PosterPath    string  `json:"poster_path"`
	Overview      string  `json:"overview"`
	// Genres are TMDB genre ids, used by the classification rules.
	Genres []string `json:"genres,omitempty"`
	// GenreNames are the same genres in the configured language, for the
	// name based blacklist. They are kept beside the ids because the ids are
	// what the rules match on and the names are what an operator recognises.
	GenreNames []string `json:"genre_names,omitempty"`
	// OriginalLanguage drives the language based rules, for example "zh".
	OriginalLanguage string `json:"original_language,omitempty"`
	// OriginCountries is a TV show's origin_country.
	OriginCountries []string `json:"origin_countries,omitempty"`
	// ProductionCountries is a movie's production_countries.
	ProductionCountries []string `json:"production_countries,omitempty"`
	// MatchedBy records how the entry was found: "imdb" or "title".
	MatchedBy string `json:"matched_by"`
	// Aliases are the alternative titles TMDB lists for the entry.
	//
	// They are what lets a release match the name a fansub group published
	// rather than the entry's own name: the group writes "雪王来了" while TMDB
	// stores "雪王驾到", and the two are the same work. Only an exact
	// normalised equality is accepted against an alias, so this widens what
	// can match without making the match fuzzy.
	Aliases []string `json:"aliases,omitempty"`
	// Confidence is 1.0 for an id match and below it for title matches.
	Confidence float64 `json:"confidence"`
}

// URL returns the public TMDB page for the entry.
func (e Entry) URL() string {
	if e.ID == 0 {
		return ""
	}
	if e.Type == "tv" {
		return fmt.Sprintf("https://www.themoviedb.org/tv/%d", e.ID)
	}
	return fmt.Sprintf("https://www.themoviedb.org/movie/%d", e.ID)
}

// PosterURL returns a poster image URL at the requested width, or "".
func (e Entry) PosterURL(width int) string {
	if e.PosterPath == "" {
		return ""
	}
	if width <= 0 {
		width = 500
	}
	return fmt.Sprintf("%s/w%d%s", ImageBase, width, e.PosterPath)
}

// Client talks to the TMDB API.
type Client struct {
	APIKey  string
	Lang    string
	HTTP    *http.Client
	baseURL string

	mu    sync.Mutex
	cache map[string]Entry
}

// New builds a client. An empty key yields a client whose lookups fail with
// ErrNotConfigured, which lets callers keep running without TMDB.
func New(apiKey, lang string) *Client {
	if strings.TrimSpace(lang) == "" {
		lang = "zh-CN"
	}
	return &Client{
		APIKey: strings.TrimSpace(apiKey),
		Lang:   lang,
		HTTP:   &http.Client{Timeout: 20 * time.Second},
		cache:  map[string]Entry{},
	}
}

// Configured reports whether an API key is present.
func (c *Client) Configured() bool { return c != nil && c.APIKey != "" }

// Resolve maps a release to a TMDB entry.
//
// The IMDb id is authoritative: when present it is tried first and its result
// is accepted without a title comparison.
//
// Without an id, the release name supplies the structural facts (year, season,
// kind) and each candidate title is searched in turn. canonicalTitle comes
// from the detail page and is tried first, because for a film it is the clean
// title with the release noise already stripped. It is not trusted blindly:
// series pages report the work's original name, which is frequently in another
// script, so the release-derived title remains as a fallback.
func (c *Client) Resolve(ctx context.Context, imdbID, releaseName, canonicalTitle string) (Entry, error) {
	if !c.Configured() {
		return Entry{}, ErrNotConfigured
	}
	parsed := media.Parse(releaseName)

	if id := strings.TrimSpace(imdbID); id != "" {
		if e, err := c.byIMDb(ctx, id, parsed.Kind); err == nil {
			return e, nil
		} else if !errors.Is(err, ErrNoMatch) {
			return Entry{}, err
		}
	}

	var lastErr error = ErrNoMatch
	for _, title := range candidateTitles(canonicalTitle, releaseName) {
		e, err := c.byTitle(ctx, title, parsed)
		if err == nil {
			return e, nil
		}
		// A transport or key problem will not be fixed by another title, so
		// it is reported immediately rather than masked by the next attempt.
		if !errors.Is(err, ErrNoMatch) {
			return Entry{}, err
		}
		lastErr = err
	}
	return Entry{}, lastErr
}

// candidateTitles returns the titles to search, in order, skipping blanks and
// duplicates. Comparison folds case and punctuation so a canonical title that
// only differs cosmetically does not trigger a second request.
//
// The canonical title comes first because, when the page states one, it is the
// title TMDB itself is most likely to store. The release-derived candidates
// follow, because Chinese animation is published as
// "[字幕组] 中文名 / Romaji / English - 第14话" and no single cleaned form of
// that matches: each alternative has to be offered in turn.
func candidateTitles(canonical, releaseName string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range append([]string{canonical}, media.SearchTitles(releaseName)...) {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := media.Normalize(t)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

// byIMDb resolves an IMDb id through the /find endpoint.
func (c *Client) byIMDb(ctx context.Context, imdbID string, want media.Kind) (Entry, error) {
	imdbID = strings.TrimSpace(imdbID)
	if !strings.HasPrefix(imdbID, "tt") {
		imdbID = "tt" + imdbID
	}
	key := "imdb:" + imdbID
	if e, ok := c.cached(key); ok {
		return e, nil
	}

	var out struct {
		MovieResults []findResult `json:"movie_results"`
		TVResults    []findResult `json:"tv_results"`
	}
	q := url.Values{
		"external_source": {"imdb_id"},
		"language":        {c.Lang},
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(imdbID), q, &out); err != nil {
		return Entry{}, err
	}

	// Prefer the type the release name implies, else fall back to whichever
	// list the API populated.
	pick := func(list []findResult, typ string) (Entry, bool) {
		if len(list) == 0 {
			return Entry{}, false
		}
		return c.fill(ctx, list[0].ID, typ, "imdb", 1.0)
	}
	if want == media.KindMovie {
		if e, ok := pick(out.MovieResults, "movie"); ok {
			c.store(key, e)
			return e, nil
		}
	}
	if want == media.KindTV || want == media.KindUnknown {
		if e, ok := pick(out.TVResults, "tv"); ok {
			c.store(key, e)
			return e, nil
		}
	}
	if e, ok := pick(out.MovieResults, "movie"); ok {
		c.store(key, e)
		return e, nil
	}
	return Entry{}, ErrNoMatch
}

type findResult struct {
	ID int `json:"id"`
}

// searchResult is one entry from a /search response.
type searchResult struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	OriginalTtl  string `json:"original_title"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
}

// ownNames lists every name the search response itself exposes, which is the
// set an exact match is tested against.
func (r searchResult) ownNames() []string {
	return []string{r.Title, r.Name, r.OriginalTitle(), r.OriginalTtl}
}

// OriginalTitle returns whichever original-name field this media type uses.
func (r searchResult) OriginalTitle() string {
	if r.OriginalName != "" {
		return r.OriginalName
	}
	return r.OriginalTtl
}

// byTitle searches TMDB by title. The year and kind come from parsed, which is
// derived from the release name, so a canonical title that differs does not
// change the search constraints.
//
// A match is accepted in two passes, and the second only runs when the first
// found nothing:
//
//  1. The entry's own name equals the query, normalised. This is the strong
//     claim and is settled from the search response alone.
//  2. The entry's alias equals the query. A fansub release publishes the name
//     TMDB lists as an alias rather than as the entry title ("雪王来了" for the
//     entry "雪王驾到", "Kimi ga Shinu made Koi wo Shitai" for "与你相恋到生命
//     尽头"), and without this pass most Chinese animation never resolves.
//
// The alias pass costs a detail request per result, so it is limited to the
// best few and an alias still has to equal the query exactly. That widens which
// name can match without making the match fuzzy.
//
// The year is deliberately not sent as a query parameter. TMDB's year filters
// are exact, and a torrent's year is not reliably the same field: for a series
// it is the air year of that episode, which is usually later than the show's
// first air date. Filtering server-side would discard valid results, so the
// year is applied here instead, and only loosely.
func (c *Client) byTitle(ctx context.Context, title string, parsed media.Result) (Entry, error) {
	kinds := []string{"movie", "tv"}
	switch parsed.Kind {
	case media.KindMovie:
		kinds = []string{"movie"}
	case media.KindTV:
		kinds = []string{"tv"}
	}

	// The search is issued once per kind. The exact pass reuses the response
	// rather than repeating the request, so the alias fallback stays cheap.
	responses := map[string][]searchResult{}
	for _, kind := range kinds {
		var out struct {
			Results []searchResult `json:"results"`
		}
		// The query is folded to simplified, because TMDB indexes the entry
		// under its zh-CN name while the release may state the traditional one.
		// Measured: 進擊的巨人 最終季 returns no results at all, and its folded
		// form returns the entries the comparison then confirms. Folding here
		// rather than in the caller is what makes it effective: candidateTitles
		// dedupes on Normalize, which already folds, so a folded variant offered
		// as an extra candidate would be discarded as a duplicate of this one.
		q := url.Values{"query": {media.ToSimplified(title)}, "language": {c.Lang}}
		if err := c.get(ctx, "/search/"+kind, q, &out); err != nil {
			return Entry{}, err
		}
		responses[kind] = out.Results
	}

	want := media.Normalize(title)
	// When the release name carried a season marker, the query is the base name
	// and the marker is what distinguishes the instalment. TMDB sometimes files
	// a season as its own entry ("毛骗 第二季") beside the base one ("毛骗"), and
	// both come back for the base query, so the marker decides.
	_, seasonMarker := media.ChineseSeasonMarker(parsed.Title)

	// Pass 1: the entry's own name.
	for _, kind := range kinds {
		var exact []searchResult
		// A candidate carrying the marker is the instalment the release names,
		// so it is resolved on its own before the others are considered. Doing
		// this for the release kind only keeps the marker from being applied to
		// a kind the release never claimed.
		if seasonMarker != "" && kind == string(parsed.Kind) {
			var marked []searchResult
			for _, r := range responses[kind] {
				if !yearCompatible(kind, parsed.Year, yearFrom(r.ReleaseDate, r.FirstAirDate)) {
					continue
				}
				if nameCarriesMarker(r.ownNames(), seasonMarker) {
					marked = append(marked, r)
				}
			}
			if e, ok := c.pickBest(ctx, kind, parsed, marked, 0.9); ok {
				return e, nil
			}
		}
		for _, r := range responses[kind] {
			if !yearCompatible(kind, parsed.Year, yearFrom(r.ReleaseDate, r.FirstAirDate)) {
				continue
			}
			if matchesAnyName(r.ownNames(), want) {
				exact = append(exact, r)
			}
		}
		if e, ok := c.pickBest(ctx, kind, parsed, exact, 0.9); ok {
			return e, nil
		}
	}

	// Pass 2: the entry's alias. Only names the search offered are tried, and
	// an alias has to equal one of them exactly, so this widens which name can
	// match without accepting whatever the search happened to rank first.
	//
	// An alias match is weaker evidence than an exact name, so a query too
	// short to identify a work is not allowed to use it. A two-letter Latin
	// query matches something for any pair of letters: "Re" resolves an entry
	// named "ARTE Re:" through its alias "Re:". Chinese names are exempt,
	// because two characters are a complete and distinctive title there
	// ("黑门").
	if !distinctiveQuery(want) {
		return Entry{}, ErrNoMatch
	}
	for _, kind := range kinds {
		var cands []searchResult
		for _, r := range responses[kind] {
			if !yearCompatible(kind, parsed.Year, yearFrom(r.ReleaseDate, r.FirstAirDate)) {
				continue
			}
			// A candidate whose own name already equals the query was handled
			// by pass 1 and did not resolve, so it is not retried here.
			if matchesAnyName(r.ownNames(), want) {
				continue
			}
			cands = append(cands, r)
			if len(cands) == aliasCandidateLimit {
				break
			}
		}
		if e, ok := c.pickAlias(ctx, kind, parsed, cands, want); ok {
			return e, nil
		}
	}
	return Entry{}, ErrNoMatch
}

// distinctiveQuery reports whether a normalised query is long enough to
// identify a work on its own.
func distinctiveQuery(want string) bool {
	r := []rune(want)
	if len(r) >= aliasMinRunes {
		return true
	}
	for _, c := range r {
		if unicode.Is(unicode.Han, c) {
			return true
		}
	}
	return false
}

// aliasMinRunes is the shortest Latin query the alias pass will accept.
const aliasMinRunes = 3

// aliasCandidateLimit bounds how many search results the alias pass resolves.
// Each costs a detail request, and an alias match is only ever found near the
// top: TMDB ranks a work highly for a name it also lists as an alias.
const aliasCandidateLimit = 5

// matchesAnyName reports whether any name normalises to want.
func matchesAnyName(names []string, want string) bool {
	if want == "" {
		return false
	}
	for _, n := range names {
		if media.Normalize(n) == want {
			return true
		}
	}
	return false
}

// nameCarriesMarker reports whether one of the names contains the season
// marker a release stated, for example "毛骗 第二季 (2011)" for "第二季".
//
// The comparison is a containment test rather than an equality one because the
// marker is part of a longer name. It is deliberately not used to accept a
// match on its own: a name carrying the marker is still resolved through the
// same exact-name check as any other, so this only changes which candidates
// are considered first.
func nameCarriesMarker(names []string, marker string) bool {
	if marker == "" {
		return false
	}
	want := media.Normalize(marker)
	if want == "" {
		return false
	}
	for _, n := range names {
		if strings.Contains(media.Normalize(n), want) {
			return true
		}
	}
	return false
}

// pickBest fills the candidates and returns the most plausible one. Candidates
// whose name already equals the query are preferred over the rest.
func (c *Client) pickBest(ctx context.Context, kind string, parsed media.Result,
	cands []searchResult, confidence float64) (Entry, bool) {

	best := Entry{}
	bestDelta, bestScore := 0, 0.0
	for _, cd := range cands {
		e, ok := c.fill(ctx, cd.ID, kind, "title", confidence)
		if !ok {
			continue
		}
		delta := abs(e.Year - parsed.Year)
		switch {
		case best.ID == 0:
			best, bestDelta, bestScore = e, delta, e.Rating
		case parsed.Year != 0 && delta < bestDelta:
			best, bestDelta, bestScore = e, delta, e.Rating
		case parsed.Year != 0 && delta == bestDelta && e.Rating > bestScore:
			best, bestScore = e, e.Rating
		case parsed.Year == 0 && e.Rating > bestScore:
			best, bestScore = e, e.Rating
		}
	}
	return best, best.ID != 0
}

// pickAlias resolves the candidates and accepts one whose alias equals the
// query. The alias is only visible after the detail request, which is why this
// cannot reuse the search response.
//
// The comparison is against the query itself, not against the candidate's own
// name: "雪王来了" was searched, TMDB's search offered an entry named
// "雪王驾到" because it lists "雪王来了" as an alias, and that alias is what
// confirms the two are the same work. Accepting the candidate merely for being
// ranked would make the match fuzzy instead of wider.
func (c *Client) pickAlias(ctx context.Context, kind string, parsed media.Result,
	cands []searchResult, want string) (Entry, bool) {

	best := Entry{}
	bestDelta, bestScore := 0, 0.0
	for _, cd := range cands {
		e, ok := c.fill(ctx, cd.ID, kind, "title", 0.8)
		if !ok {
			continue
		}
		if !aliasMatches(e, want) {
			continue
		}
		delta := abs(e.Year - parsed.Year)
		switch {
		case best.ID == 0:
			best, bestDelta, bestScore = e, delta, e.Rating
		case parsed.Year != 0 && delta < bestDelta:
			best, bestDelta, bestScore = e, delta, e.Rating
		case parsed.Year != 0 && delta == bestDelta && e.Rating > bestScore:
			best, bestScore = e, e.Rating
		case parsed.Year == 0 && e.Rating > bestScore:
			best, bestScore = e, e.Rating
		}
	}
	return best, best.ID != 0
}

// aliasMatches reports whether any of the entry's aliases normalises to want.
func aliasMatches(e Entry, want string) bool {
	if want == "" {
		return false
	}
	for _, a := range e.Aliases {
		if media.Normalize(a) == want {
			return true
		}
	}
	return false
}

// yearCompatible reports whether an entry's year can plausibly be the work the
// release name refers to.
//
// Films are matched within a year, to absorb the difference between a festival
// premiere and a wide release. A series needs a looser rule: its year in a
// torrent name is the air year of that episode, so the entry merely has to
// have started no later than the release.
func yearCompatible(kind string, releaseYear, entryYear int) bool {
	if releaseYear == 0 || entryYear == 0 {
		return true
	}
	if kind == "tv" {
		return entryYear <= releaseYear+1
	}
	delta := releaseYear - entryYear
	return delta >= -1 && delta <= 1
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// fill loads full details for an id so title, year and rating are populated.
func (c *Client) fill(ctx context.Context, id int, kind, matchedBy string, confidence float64) (Entry, bool) {
	var raw struct {
		ID            int      `json:"id"`
		Title         string   `json:"title"`
		Name          string   `json:"name"`
		OriginalTitle string   `json:"original_title"`
		OriginalName  string   `json:"original_name"`
		ReleaseDate   string   `json:"release_date"`
		FirstAirDate  string   `json:"first_air_date"`
		VoteAverage   float64  `json:"vote_average"`
		VoteCount     int      `json:"vote_count"`
		PosterPath    string   `json:"poster_path"`
		Overview      string   `json:"overview"`
		OriginalLang  string   `json:"original_language"`
		OriginCountry []string `json:"origin_country"`
		Genres        []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"genres"`
		ProductionCountries []struct {
			ISO string `json:"iso_3166_1"`
		} `json:"production_countries"`
		// AlternativeTitles rides along on the same request rather than
		// costing a second one. The aliases are what a fansub release actually
		// publishes, so they are the difference between matching and not.
		AlternativeTitles struct {
			Results []struct {
				Title string `json:"title"`
			} `json:"results"`
		} `json:"alternative_titles"`
	}
	q := url.Values{
		"language":           {c.Lang},
		"append_to_response": {"alternative_titles"},
	}
	if err := c.get(ctx, "/"+kind+"/"+strconv.Itoa(id), q, &raw); err != nil {
		return Entry{}, false
	}
	title := raw.Title
	original := raw.OriginalTitle
	if title == "" {
		title = raw.Name
		original = raw.OriginalName
	}
	genres := make([]string, 0, len(raw.Genres))
	genreNames := make([]string, 0, len(raw.Genres))
	for _, g := range raw.Genres {
		genres = append(genres, strconv.Itoa(g.ID))
		if g.Name != "" {
			genreNames = append(genreNames, g.Name)
		}
	}
	countries := make([]string, 0, len(raw.ProductionCountries))
	for _, c := range raw.ProductionCountries {
		if c.ISO != "" {
			countries = append(countries, c.ISO)
		}
	}
	aliases := make([]string, 0, len(raw.AlternativeTitles.Results))
	seenAlias := map[string]bool{}
	for _, a := range raw.AlternativeTitles.Results {
		a.Title = strings.TrimSpace(a.Title)
		if a.Title == "" || seenAlias[a.Title] {
			continue
		}
		seenAlias[a.Title] = true
		aliases = append(aliases, a.Title)
	}
	return Entry{
		ID:            raw.ID,
		Type:          kind,
		Title:         title,
		OriginalTitle: original,
		Year:          yearFrom(raw.ReleaseDate, raw.FirstAirDate),
		Rating:        raw.VoteAverage,
		VoteCount:     raw.VoteCount,
		PosterPath:    raw.PosterPath,
		Overview:      raw.Overview,
		Genres:        genres,
		GenreNames:    genreNames,
		// The upstream field is singular; keeping the plural name here matches
		// the rule syntax, which accepts several values for one field.
		OriginalLanguage:    raw.OriginalLang,
		OriginCountries:     raw.OriginCountry,
		ProductionCountries: countries,
		MatchedBy:           matchedBy,
		Aliases:             aliases,
		Confidence:          confidence,
	}, true
}

// yearFrom extracts the leading four digit year from a TMDB date string.
func yearFrom(dates ...string) int {
	for _, d := range dates {
		if len(d) >= 4 {
			if y, err := strconv.Atoi(d[:4]); err == nil && y > 1800 {
				return y
			}
		}
	}
	return 0
}

func (c *Client) cached(key string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	return e, ok
}

func (c *Client) store(key string, e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bound the cache; the working set per run is small, so a simple reset is
	// adequate and keeps memory flat over long uptimes.
	if len(c.cache) > 512 {
		c.cache = map[string]Entry{}
	}
	c.cache[key] = e
}

// get performs an authenticated GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	base := c.baseURL
	if base == "" {
		base = BaseURL
	}
	// A v4 token is a JWT and must travel in the Authorization header; a v3
	// key goes in the query string.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if looksLikeV4(c.APIKey) {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	} else {
		qs := req.URL.Query()
		qs.Set("api_key", c.APIKey)
		req.URL.RawQuery = qs.Encode()
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNoMatch
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: 401", ErrUnauthorized)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: 429", ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb HTTP %d: %s", resp.StatusCode, truncate(string(body), 160))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tmdb: cannot decode response: %w", err)
	}
	return nil
}

// looksLikeV4 reports whether the credential is a v4 JWT bearer token.
func looksLikeV4(key string) bool {
	return strings.Count(key, ".") == 2 && len(key) > 40
}

// SetBaseURL overrides the API origin, used by tests.
func (c *Client) SetBaseURL(u string) { c.baseURL = strings.TrimSuffix(u, "/") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
