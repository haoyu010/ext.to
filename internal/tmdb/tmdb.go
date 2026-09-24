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

// byTitle searches TMDB by title and accepts only a normalised exact match.
// The year and kind come from parsed, which is derived from the release name,
// so a canonical title that differs does not change the search constraints.
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

	want := media.Normalize(title)
	var best Entry
	var bestDelta int
	var bestScore float64
	for _, kind := range kinds {
		var out struct {
			Results []struct {
				ID           int    `json:"id"`
				Title        string `json:"title"`
				Name         string `json:"name"`
				OriginalName string `json:"original_name"`
				OriginalTtl  string `json:"original_title"`
				ReleaseDate  string `json:"release_date"`
				FirstAirDate string `json:"first_air_date"`
			} `json:"results"`
		}
		q := url.Values{"query": {title}, "language": {c.Lang}}
		if err := c.get(ctx, "/search/"+kind, q, &out); err != nil {
			return Entry{}, err
		}
		for _, r := range out.Results {
			name := r.Title
			if name == "" {
				name = r.Name
			}
			if media.Normalize(name) != want {
				continue
			}
			y := yearFrom(r.ReleaseDate, r.FirstAirDate)
			if !yearCompatible(kind, parsed.Year, y) {
				continue
			}
			e, ok := c.fill(ctx, r.ID, kind, "title", 0.9)
			if !ok {
				continue
			}
			// Prefer the candidate whose year is closest to the release,
			// which is what disambiguates a remake. Without a release year,
			// fall back to the better-known entry.
			delta := abs(e.Year - parsed.Year)
			switch {
			case best.ID == 0:
				best = e
				bestDelta, bestScore = delta, e.Rating
			case parsed.Year != 0 && delta < bestDelta:
				best = e
				bestDelta, bestScore = delta, e.Rating
			case parsed.Year != 0 && delta == bestDelta && e.Rating > bestScore:
				best = e
				bestScore = e.Rating
			case parsed.Year == 0 && e.Rating > bestScore:
				best = e
				bestScore = e.Rating
			}
		}
		if best.ID != 0 {
			return best, nil
		}
	}
	return Entry{}, ErrNoMatch
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
	}
	q := url.Values{"language": {c.Lang}}
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
