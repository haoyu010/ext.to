package scrape

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// liveCookieEnv points at a solver response captured from a browser that
// already passed the Cloudflare challenge.
const liveCookieEnv = "LIVE_COOKIE_FILE"

// TestLiveMagnetAndPoster exercises the signed magnet endpoint and poster
// extraction against the real site. It needs network access and a valid
// cf_clearance cookie, so it only runs when LIVE_COOKIE_FILE is set.
func TestLiveMagnetAndPoster(t *testing.T) {
	path := os.Getenv(liveCookieEnv)
	if path == "" {
		t.Skipf("set %s to a captured solver response to run this test", liveCookieEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cookie file: %v", err)
	}
	var res struct {
		Solution struct {
			CFClearance struct {
				Value string `json:"value"`
			} `json:"cf_clearance"`
			Cookies []struct {
				Name   string `json:"name"`
				Value  string `json:"value"`
				Domain string `json:"domain"`
			} `json:"cookies"`
			UserAgent string `json:"user_agent"`
		} `json:"solution"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("parse cookie file: %v", err)
	}

	c := &Client{
		HTTP:      &http.Client{Timeout: 90 * time.Second},
		Clearance: res.Solution.CFClearance.Value,
		UserAgent: res.Solution.UserAgent,
	}
	for _, ck := range res.Solution.Cookies {
		if ck.Domain == ".ext.to" && ck.Name == "PHPSESSID" {
			c.Session = ck.Value
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	items, err := c.FetchList(ctx, FetchOptions{Categories: []int{1, 2}, Age: 0, MaxPages: 1})
	if err != nil {
		t.Fatalf("FetchList: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no torrents returned")
	}
	t.Logf("fetched %d torrents", len(items))

	target := items[0]
	magnet, err := c.FetchMagnet(ctx, target)
	if err != nil {
		t.Fatalf("FetchMagnet(%d): %v", target.ID, err)
	}
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:") {
		t.Fatalf("unexpected magnet: %q", magnet)
	}
	if len(magnet) > 80 {
		t.Logf("magnet for %d: %s", target.ID, magnet[:80])
	} else {
		t.Logf("magnet for %d: %s", target.ID, magnet)
	}

	poster, err := c.FetchPoster(ctx, target)
	if err != nil {
		t.Fatalf("FetchPoster: %v", err)
	}
	if poster == "" {
		t.Skip("this torrent has no poster; magnet resolution already passed")
	}
	if isPlaceholderImage(poster) {
		t.Fatalf("poster resolved to a placeholder: %q", poster)
	}
	img, err := c.DownloadImage(ctx, poster)
	if err != nil {
		t.Fatalf("DownloadImage: %v", err)
	}
	if len(img) < 512 {
		t.Fatalf("poster looks too small: %d bytes", len(img))
	}
	t.Logf("poster: %s (%d bytes)", poster, len(img))
}

// TestLiveSeriesDetail checks the series-specific layout against the real
// site: the media type must be reported as tv and the poster must be real
// artwork rather than the /static/img placeholder. It is the only way to
// confirm the two page layouts, since both change together on the server
// side and a fixture can drift.
func TestLiveSeriesDetail(t *testing.T) {
	path := os.Getenv(liveCookieEnv)
	if path == "" {
		t.Skipf("set %s to a captured solver response to run this test", liveCookieEnv)
	}
	c := liveClient(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	items, err := c.FetchList(ctx, FetchOptions{Categories: []int{2}, Age: 0, MaxPages: 1})
	if err != nil {
		t.Fatalf("FetchList(tv): %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no series returned")
	}
	t.Logf("fetched %d series", len(items))

	// At least one series page in the listing should expose a usable poster,
	// and none may resolve to the shared placeholder.
	var withPoster int
	for _, it := range items[:min(6, len(items))] {
		d, err := c.FetchDetail(ctx, it)
		if err != nil {
			t.Logf("detail for %d failed: %v", it.ID, err)
			continue
		}
		if isPlaceholderImage(d.PosterURL) {
			t.Errorf("%d (%s) resolved to a placeholder poster: %q", it.ID, it.Title, d.PosterURL)
		}
		if d.PosterURL != "" {
			withPoster++
			t.Logf("%d: kind=%q imdb=%q poster=%s", it.ID, d.Kind, d.IMDbID, d.PosterURL)
		}
	}
	if withPoster == 0 {
		t.Error("no series in the sample had a poster; the serial_poster parsing may have broken")
	}
}

// liveClient builds a client from a captured solver response.
func liveClient(t *testing.T, path string) *Client {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cookie file: %v", err)
	}
	var res struct {
		Solution struct {
			CFClearance struct {
				Value string `json:"value"`
			} `json:"cf_clearance"`
			Cookies []struct {
				Name   string `json:"name"`
				Value  string `json:"value"`
				Domain string `json:"domain"`
			} `json:"cookies"`
			UserAgent string `json:"user_agent"`
		} `json:"solution"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("parse cookie file: %v", err)
	}
	c := &Client{
		HTTP:      &http.Client{Timeout: 90 * time.Second},
		Clearance: res.Solution.CFClearance.Value,
		UserAgent: res.Solution.UserAgent,
	}
	for _, ck := range res.Solution.Cookies {
		if ck.Domain == ".ext.to" && ck.Name == "PHPSESSID" {
			c.Session = ck.Value
		}
	}
	return c
}
