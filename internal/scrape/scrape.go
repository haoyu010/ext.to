// Package scrape reads torrent listings and detail pages from ext.to.
//
// Requests rely on a cf_clearance cookie harvested out-of-band from a real
// browser. Cloudflare binds that cookie to the client IP and User-Agent, so
// both must stay stable for requests to succeed.
package scrape

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/haoyu010/ext.to/internal/config"
)

// BaseURL is the canonical origin. It is a variable so tests can point it at
// a local fixture server.
var BaseURL = "https://ext.to"

const (
	// listPath renders the age-filtered, newest-first listing.
	listPath = "/browse/?sort=age&order=desc"
	// magnetPath issues signed magnet links.
	magnetPath = "/ajax/getTorrentMagnet.php"
)

// ErrBlocked indicates Cloudflare served a challenge instead of content,
// which almost always means the clearance cookie is missing, stale, or bound
// to a different IP.
var ErrBlocked = errors.New("cloudflare challenge returned; refresh cf_clearance")

// Item is a torrent listing entry.
type Item struct {
	ID       int
	Slug     string
	Title    string
	URL      string
	Category string
	Uploader string
	Size     string
	SizeMB   float64
	Files    int
	Age      string
	Seeds    int
	Leeches  int
}

// Client fetches ext.to pages using a stored clearance cookie.
type Client struct {
	HTTP      *http.Client
	Clearance string
	Session   string
	UserAgent string
}

// New builds a client from settings, including optional proxy support.
func New(s config.Settings) (*Client, error) {
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
	}
	if p := strings.TrimSpace(s.Proxy); p != "" {
		pu, err := url.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy %q: %w", p, err)
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	return &Client{
		HTTP:      &http.Client{Transport: tr, Timeout: 60 * time.Second},
		Clearance: s.Clearance,
		Session:   s.Session,
		UserAgent: s.UserAgent,
	}, nil
}

// FetchOptions controls how a listing scan walks pages.
type FetchOptions struct {
	Categories []int
	Age        int
	MaxPages   int
	// Delay is slept between page requests to stay polite.
	Delay time.Duration
}

// FetchList returns the newest torrents across the requested categories.
// Results are de-duplicated by id.
func (c *Client) FetchList(ctx context.Context, opt FetchOptions) ([]Item, error) {
	if opt.MaxPages < 1 {
		opt.MaxPages = 1
	}
	cats := opt.Categories
	if len(cats) == 0 {
		cats = []int{config.CatAll}
	}

	seen := map[int]bool{}
	var out []Item
	for _, cat := range cats {
		for page := 1; page <= opt.MaxPages; page++ {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			u := fmt.Sprintf("%s%s&age=%d&cat=%d&page=%d", BaseURL, listPath, opt.Age, cat, page)
			body, err := c.get(ctx, u)
			if err != nil {
				return out, fmt.Errorf("category %s page %d: %w",
					config.CategoryNames[cat], page, err)
			}
			items, err := ParseList(body)
			if err != nil {
				return out, fmt.Errorf("category %s page %d: %w",
					config.CategoryNames[cat], page, err)
			}
			if len(items) == 0 {
				break
			}
			added := 0
			for _, it := range items {
				if seen[it.ID] {
					continue
				}
				seen[it.ID] = true
				out = append(out, it)
				added++
			}
			// A page that adds nothing new means we have caught up.
			if added == 0 {
				break
			}
			if opt.Delay > 0 {
				time.Sleep(opt.Delay)
			}
		}
	}
	return out, nil
}

// FetchMagnet resolves the signed magnet link for a torrent by reading the
// detail page (which carries the per-page token) and calling the JSON endpoint.
func (c *Client) FetchMagnet(ctx context.Context, item Item) (string, error) {
	body, err := c.get(ctx, c.detailURL(item))
	if err != nil {
		return "", err
	}
	return c.MagnetFromDetail(ctx, item, body)
}

// MagnetFromDetail resolves the magnet link using an already-fetched detail
// page. The page token is bound to both the torrent and that specific page
// load, so the body must belong to this item. Passing a body in saves a
// second request when the caller already read the page for other metadata.
func (c *Client) MagnetFromDetail(ctx context.Context, item Item, body []byte) (string, error) {
	detailURL := c.detailURL(item)
	token, csrf := parseTokens(body)
	if token == "" || csrf == "" {
		return "", errors.New("page token not found; page layout may have changed")
	}

	ts := time.Now().Unix()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%d|%s", item.ID, ts, token)))
	form := url.Values{
		"torrent_id":    {strconv.Itoa(item.ID)},
		"download_type": {"magnet"},
		"timestamp":     {strconv.FormatInt(ts, 10)},
		"hmac":          {hex.EncodeToString(sum[:])},
		"sessid":        {csrf},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+magnetPath,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	c.decorate(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", detailURL)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var out struct {
		Success bool   `json:"success"`
		URL     string `json:"url"`
		Hash    string `json:"hash"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("unexpected magnet response: %s", truncate(string(raw), 120))
	}
	if !out.Success {
		if out.Error == "" {
			out.Error = "unknown error"
		}
		return "", fmt.Errorf("magnet rejected: %s", out.Error)
	}
	// The endpoint escapes forward slashes in tracker URLs.
	if out.URL != "" {
		return strings.ReplaceAll(out.URL, `\/`, `/`), nil
	}
	if out.Hash != "" {
		return "magnet:?xt=urn:btih:" + out.Hash, nil
	}
	return "", errors.New("magnet endpoint returned no link")
}

// Detail holds the metadata scraped from a torrent's detail page.
type Detail struct {
	// PosterURL is the full-size poster, when the page has one.
	PosterURL string
	// IMDbID is an id such as "tt6473542", when the page declares one. It is
	// the most reliable bridge to TMDB, so it is preferred over title search.
	IMDbID string
	// Title is the canonical work title from the page's info list, for
	// example "Glass Onion: A Knives Out Mystery". Empty when absent.
	Title string
	// Kind is "movie" or "tv" when the page declares the media type.
	Kind string
}

// Info page labels that state the canonical title and media type.
var infoTitleLabels = map[string]string{
	"movie":     "movie",
	"tv show":   "tv",
	"tv":        "tv",
	"series":    "tv",
	"tv series": "tv",
}

// FetchDetailPage reads a torrent's detail page and returns the raw body.
//
// Callers that need more than one derived value should use this together with
// ParseDetail and MagnetFromDetail: the magnet endpoint's page token is bound
// to a single page load, so reusing one body saves a request and guarantees
// the token matches the item being published.
func (c *Client) FetchDetailPage(ctx context.Context, item Item) ([]byte, error) {
	return c.get(ctx, c.detailURL(item))
}

// ParseDetail extracts the metadata the forwarder needs from a detail page.
func ParseDetail(body []byte) Detail {
	d := Detail{
		PosterURL: parsePosterURL(body),
		IMDbID:    parseIMDbID(body),
	}
	d.Title, d.Kind = parseInfoTitle(body)
	return d
}

// FetchDetail reads a detail page once and extracts every field the
// forwarder needs, so a torrent costs a single extra request.
func (c *Client) FetchDetail(ctx context.Context, item Item) (Detail, error) {
	body, err := c.FetchDetailPage(ctx, item)
	if err != nil {
		return Detail{}, err
	}
	return ParseDetail(body), nil
}

// FetchPoster returns the best available poster image URL for a detail page,
// preferring the full-size asset over the generated thumbnail.
func (c *Client) FetchPoster(ctx context.Context, item Item) (string, error) {
	body, err := c.get(ctx, c.detailURL(item))
	if err != nil {
		return "", err
	}
	return parsePosterURL(body), nil
}

// DownloadImage fetches remote bytes, used to re-upload posters to Telegram.
func (c *Client) DownloadImage(ctx context.Context, rawURL string) ([]byte, error) {
	if rawURL == "" {
		return nil, errors.New("empty image url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	c.decorate(req)
	req.Header.Set("Referer", BaseURL+"/")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch: HTTP %d", resp.StatusCode)
	}
	// Posters are small; 12 MiB is a generous cap that still bounds memory.
	return io.ReadAll(io.LimitReader(resp.Body, 12<<20))
}

func (c *Client) detailURL(item Item) string {
	if item.URL != "" {
		return item.URL
	}
	return fmt.Sprintf("%s/%s/", BaseURL, strings.Trim(item.Slug, "/"))
}

func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.decorate(req)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if IsChallenge(body) {
		return nil, ErrBlocked
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func (c *Client) decorate(req *http.Request) {
	ua := c.UserAgent
	if ua == "" {
		ua = config.DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	var ck []string
	if c.Clearance != "" {
		ck = append(ck, "cf_clearance="+c.Clearance)
	}
	if c.Session != "" {
		ck = append(ck, "PHPSESSID="+c.Session)
	}
	if len(ck) > 0 {
		req.Header.Set("Cookie", strings.Join(ck, "; "))
	}
}

var challengeMarkers = [][]byte{
	[]byte("Just a moment"),
	[]byte("Performing security verification"),
	[]byte("__cf_chl_"),
	[]byte("challenge-platform"),
}

// challengeScanLimit bounds how much of a body is searched for markers. Real
// listing pages are far larger than a challenge interstitial.
const challengeScanLimit = 400_000

// IsChallenge reports whether the response body is a Cloudflare interstitial.
func IsChallenge(body []byte) bool {
	probe := body
	if len(probe) > challengeScanLimit {
		probe = probe[:challengeScanLimit]
	}
	for _, m := range challengeMarkers {
		if bytes.Contains(probe, m) {
			return true
		}
	}
	return false
}

// ParseList extracts listing rows from a browse page.
func ParseList(body []byte) ([]Item, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var items []Item
	forEachNode(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "tr" {
			return
		}
		if it, ok := parseRow(n); ok {
			items = append(items, it)
		}
	})
	return items, nil
}

// parseRow converts one <tr> of the listing table into an Item.
func parseRow(tr *html.Node) (Item, bool) {
	var it Item

	// The title link carries the slug in href and the title in the anchor text.
	if a := findDescendant(tr, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "torrent-title-link")
	}); a != nil {
		href := attr(a, "href")
		it.Slug = strings.Trim(href, "/")
		it.URL = absURL(href)
		it.Title = strings.TrimSpace(textOf(a))
		if t := attr(a, "data-tooltip"); t != "" {
			it.Title = strings.TrimSpace(t)
		}
		if id, ok := idFromSlug(it.Slug); ok {
			it.ID = id
		}
	}
	if it.ID == 0 {
		// Fall back to the download button's data-id, which is always present.
		if btn := findDescendant(tr, func(n *html.Node) bool {
			return n.Type == html.ElementNode && hasClass(n, "dwn-btn") && attr(n, "data-id") != ""
		}); btn != nil {
			it.ID, _ = strconv.Atoi(attr(btn, "data-id"))
		}
	}
	if it.ID == 0 || it.Title == "" {
		return it, false
	}

	// Row cells expose "Size", "Files", "Age", "Seeds", "Leechs" label pairs.
	forEachNode(tr, func(n *html.Node) {
		if n.Type != html.ElementNode || !hasClass(n, "add-block-wrapper") {
			return
		}
		label, value := wrapperKV(n)
		switch strings.ToLower(label) {
		case "size":
			it.Size = value
			if mb, ok := config.ParseSizeMB(value); ok {
				it.SizeMB = mb
			}
		case "files":
			it.Files, _ = strconv.Atoi(digits(value))
		case "age":
			it.Age = value
		case "seeds":
			it.Seeds, _ = strconv.Atoi(digits(value))
		case "leechs":
			it.Leeches, _ = strconv.Atoi(digits(value))
		}
	})

	// Category breadcrumb: "Posted by DHT in Movies - Highres Movies".
	if rp := findDescendant(tr, func(n *html.Node) bool {
		return n.Type == html.ElementNode && hasClass(n, "related-posted")
	}); rp != nil {
		text := strings.Join(strings.Fields(textOf(rp)), " ")
		if i := strings.Index(text, " in "); i >= 0 {
			it.Category = strings.TrimSpace(text[i+4:])
		}
		if i := strings.Index(text, "Posted by "); i >= 0 {
			rest := text[i+len("Posted by "):]
			if j := strings.Index(rest, " in "); j >= 0 {
				it.Uploader = strings.TrimSpace(rest[:j])
			}
		}
	}
	return it, true
}

var (
	reToken  = regexp.MustCompile(`window\.pageToken\s*=\s*'([^']+)'`)
	reCSRF   = regexp.MustCompile(`name="csrf-token"\s+content="([^"]+)"`)
	reSlugID = regexp.MustCompile(`-(\d{6,})$`)
	rePoster = regexp.MustCompile(`class="[^"]*detail-torrent-image[^"]*"[^>]*src="([^"]+)"`)
	reThumb  = regexp.MustCompile(`/resize_cache/`)
	reDimDir = regexp.MustCompile(`/\d+_\d+_\d+/`)
)

func parseTokens(body []byte) (token, csrf string) {
	if m := reToken.FindSubmatch(body); m != nil {
		token = string(m[1])
	}
	if m := reCSRF.FindSubmatch(body); m != nil {
		csrf = string(m[1])
	}
	return token, csrf
}

// parsePosterURL prefers the original poster over the resized thumbnail.
func parsePosterURL(body []byte) string {
	m := rePoster.FindSubmatch(body)
	if m == nil {
		return ""
	}
	p := string(m[1])
	if !strings.HasPrefix(p, "http") {
		p = BaseURL + p
	}
	if !reThumb.MatchString(p) {
		return p
	}
	// /upload_files/resize_cache/torrents-imdb-posters/0da/174_260_2/x.jpg
	// becomes /upload_files/torrents-imdb-posters/0da/x.jpg
	alt := strings.Replace(p, "/resize_cache", "", 1)
	alt = reDimDir.ReplaceAllString(alt, "/")
	alt = strings.Replace(alt, "://", "\x00", 1)
	alt = strings.ReplaceAll(alt, "//", "/")
	alt = strings.Replace(alt, "\x00", "://", 1)
	return alt
}

func idFromSlug(slug string) (int, bool) {
	m := reSlugID.FindStringSubmatch(slug)
	if m == nil {
		return 0, false
	}
	id, err := strconv.Atoi(m[1])
	return id, err == nil
}

// parseIMDbID returns the IMDb title id declared on a detail page.
//
// Only the info list is consulted. ext.to renders "IMDb link:" next to a
// link to imdb.com/title/{id}, and that labelled link is the reliable source;
// scanning the whole document would also pick up ids from the "you may also
// like" sidebar and attribute a stranger's film to this torrent.
func parseIMDbID(body []byte) string {
	var found string
	forEachInfoItem(body, func(label string, li, _ *html.Node) {
		if found != "" || !strings.HasPrefix(label, "imdb link") {
			return
		}
		if a := findDescendant(li, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" &&
				strings.Contains(attr(n, "href"), "imdb.com/title/")
		}); a != nil {
			if m := reIMDbID.FindStringSubmatch(attr(a, "href")); m != nil {
				found = m[1]
			}
		}
	})
	return found
}

// parseInfoTitle returns the canonical title and media kind from the detail
// page's info list, for example "Movie: Ring Ring" or "TV Show: ...".
func parseInfoTitle(body []byte) (title, kind string) {
	forEachInfoItem(body, func(label string, li, strong *html.Node) {
		k, ok := infoTitleLabels[label]
		if !ok || title != "" {
			return
		}
		// Drop the leading "Movie:" label. Splitting on the first colon would
		// break titles that legitimately contain one, such as
		// "Glass Onion: A Knives Out Mystery".
		full := strings.TrimSpace(textOf(li))
		labelText := strings.TrimSpace(textOf(strong))
		title = strings.TrimSpace(strings.TrimPrefix(full, labelText))
		kind = k
	})
	return title, kind
}

// forEachInfoItem walks the <li><strong>Label:</strong> ...</li> entries of
// the detail page info list, handing each label (lowercased, colon removed)
// plus its <li> and <strong> nodes to fn.
func forEachInfoItem(body []byte, fn func(label string, li, strong *html.Node)) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return
	}
	forEachNode(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "li" {
			return
		}
		strong := findDescendant(n, func(c *html.Node) bool {
			return c.Type == html.ElementNode && c.Data == "strong"
		})
		if strong == nil {
			return
		}
		label := strings.ToLower(strings.TrimSuffix(
			strings.TrimSpace(textOf(strong)), ":"))
		if label != "" {
			fn(label, n, strong)
		}
	})
}

var reIMDbID = regexp.MustCompile(`imdb\.com/title/(tt\d{5,})`)

func absURL(href string) string {
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "http") {
		return href
	}
	return BaseURL + "/" + strings.Trim(href, "/") + "/"
}

// wrapperKV reads a <span>label</span><span>value</span> pair.
func wrapperKV(n *html.Node) (label, value string) {
	var spans []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "span" {
			spans = append(spans, c)
		}
	}
	if len(spans) < 2 {
		return "", ""
	}
	return strings.TrimSpace(textOf(spans[0])), strings.TrimSpace(textOf(spans[1]))
}

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key != "class" {
			continue
		}
		for _, f := range strings.Fields(a.Val) {
			if f == class {
				return true
			}
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	forEachNode(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	})
	return b.String()
}

func findDescendant(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	forEachNode(n, func(c *html.Node) {
		if found == nil && c != n && match(c) {
			found = c
		}
	})
	return found
}

func forEachNode(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		forEachNode(c, fn)
	}
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
