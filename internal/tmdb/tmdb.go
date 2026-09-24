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

// Resolve maps a release name and an optional IMDb id to a TMDB entry.
//
// The IMDb id is authoritative: when present it is tried first and its result
// is accepted without a title comparison. A title search is only attempted
// when no id is available, and it must agree on both title and type.
func (c *Client) Resolve(ctx context.Context, imdbID, releaseName string) (Entry, error) {
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
	if parsed.Title == "" {
		return Entry{}, ErrNoMatch
	}
	return c.byTitle(ctx, parsed)
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
func (c *Client) byTitle(ctx context.Context, parsed media.Result) (Entry, error) {
	kinds := []string{"movie", "tv"}
	switch parsed.Kind {
	case media.KindMovie:
		kinds = []string{"movie"}
	case media.KindTV:
		kinds = []string{"tv"}
	}

	want := media.Normalize(parsed.Title)
	var best Entry
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
		q := url.Values{"query": {parsed.Title}, "language": {c.Lang}}
		if parsed.Year != 0 {
			if kind == "movie" {
				q.Set("year", strconv.Itoa(parsed.Year))
			} else {
				q.Set("first_air_date_year", strconv.Itoa(parsed.Year))
			}
		}
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
			// A year in the release name must agree with the entry, when
			// both sides actually carry one.
			if y := yearFrom(r.ReleaseDate, r.FirstAirDate); parsed.Year != 0 && y != 0 && y != parsed.Year {
				continue
			}
			e, ok := c.fill(ctx, r.ID, kind, "title", 0.9)
			if !ok {
				continue
			}
			if best.ID == 0 || e.Rating > best.Rating {
				best = e
			}
		}
		if best.ID != 0 {
			return best, nil
		}
	}
	return Entry{}, ErrNoMatch
}

// fill loads full details for an id so title, year and rating are populated.
func (c *Client) fill(ctx context.Context, id int, kind, matchedBy string, confidence float64) (Entry, bool) {
	var raw struct {
		ID            int     `json:"id"`
		Title         string  `json:"title"`
		Name          string  `json:"name"`
		OriginalTitle string  `json:"original_title"`
		OriginalName  string  `json:"original_name"`
		ReleaseDate   string  `json:"release_date"`
		FirstAirDate  string  `json:"first_air_date"`
		VoteAverage   float64 `json:"vote_average"`
		VoteCount     int     `json:"vote_count"`
		PosterPath    string  `json:"poster_path"`
		Overview      string  `json:"overview"`
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
		MatchedBy:     matchedBy,
		Confidence:    confidence,
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
		return errors.New("tmdb rejected the api key (401); check the key")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("tmdb rate limit reached (429); slow the scan down")
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
