package scrape

import (
	"os"
	"path/filepath"
	"testing"
)

// syntheticDetail exercises the info-list parsing without committing a
// multi-megabyte page snapshot.
const syntheticDetail = `<!doctype html><html><body>
<div class="row movie-info"><div class="col-md-6">
<ul class="detail-page-info-list">
  <li><strong>Movie:</strong> <a href="/glass-onion-m128650/"><span>Glass Onion: A Knives Out Mystery</span></a></li>
  <li><strong>Detected quality:</strong> <span>720p (WEB-DL, Atmos)</span></li>
  <li>
    <strong>IMDb link:</strong>
    <a rel="nofollow" target="_blank" href="https://www.imdb.com/title/tt11564570/">11564570</a>
    <a href="/browse/?sort=age&amp;order=desc&amp;imdb_id=tt11564570"><span class="imdbButton">Search</span></a>
  </li>
  <li><strong>IMDb rating:</strong> 7.1 (533,052 votes)</li>
</ul>
</div>
<div class="col-md-6">
  <img class="detail-torrent-image" src="/upload_files/resize_cache/torrents-imdb-posters/fa2/174_260_2/abc.jpg">
  <ul class="detail-page-info-list">
    <li><strong>IMDb link:</strong> <a href="https://www.imdb.com/title/tt0000001/">sidebar decoy</a></li>
  </ul>
</div>
</div></body></html>`

func TestParseIMDbIDUsesLabelledInfoItem(t *testing.T) {
	if got := parseIMDbID([]byte(syntheticDetail)); got != "tt11564570" {
		t.Errorf("parseIMDbID = %q, want tt11564570", got)
	}
}

func TestParseInfoTitleKeepsColon(t *testing.T) {
	title, kind := parseInfoTitle([]byte(syntheticDetail))
	if title != "Glass Onion: A Knives Out Mystery" {
		t.Errorf("title = %q, want the full title with colon", title)
	}
	if kind != "movie" {
		t.Errorf("kind = %q, want movie", kind)
	}
}

func TestParseDetailTitleAbsent(t *testing.T) {
	title, kind := parseInfoTitle([]byte(`<html><body><ul><li><strong>Size:</strong> 1 GB</li></ul></body></html>`))
	if title != "" || kind != "" {
		t.Errorf("got %q/%q, want empty", title, kind)
	}
}

// The live fixtures are large captures kept outside the repository. When
// SCRAPE_FIXTURE_DIR points at them, the parsers run against real markup.
func TestParseLiveDetailFixtures(t *testing.T) {
	dir := os.Getenv("SCRAPE_FIXTURE_DIR")
	if dir == "" {
		t.Skip("set SCRAPE_FIXTURE_DIR to run against captured pages")
	}
	cases := map[string]struct{ imdb, title, kind string }{
		"detail_rip-22395007.html": {
			imdb: "tt6473542", title: "Ring Ring", kind: "movie",
		},
		"detail_une-22395846.html": {
			imdb: "tt11564570", title: "Glass Onion: A Knives Out Mystery", kind: "movie",
		},
	}
	for name, want := range cases {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if got := parseIMDbID(body); got != want.imdb {
			t.Errorf("%s: imdb = %q, want %q", name, got, want.imdb)
		}
		title, kind := parseInfoTitle(body)
		if title != want.title {
			t.Errorf("%s: title = %q, want %q", name, title, want.title)
		}
		if kind != want.kind {
			t.Errorf("%s: kind = %q, want %q", name, kind, want.kind)
		}
	}
}
