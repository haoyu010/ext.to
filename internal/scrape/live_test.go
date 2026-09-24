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
	img, err := c.DownloadImage(ctx, poster)
	if err != nil {
		t.Fatalf("DownloadImage: %v", err)
	}
	if len(img) < 512 {
		t.Fatalf("poster looks too small: %d bytes", len(img))
	}
	t.Logf("poster: %s (%d bytes)", poster, len(img))
}
