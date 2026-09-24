package scrape

import (
	"os"
	"path/filepath"
	"strings"
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
// seriesDetail mirrors the shape of a real ext.to series page: no "Movie:"
// row, a placeholder in detail-torrent-image, and the artwork supplied as a
// TMDB background image.
const seriesDetail = `<!doctype html><html><body>
<div class="col-lg-2 col-md-12 post-wrapper">
  <div class="poster-block">
    <div class="serial_poster__border1" style="background-image:url(https://image.tmdb.org/t/p/w300_and_h450_bestv2/orV0.jpg);"></div>
    <div class="serial_poster__border2" style="background-image:url(https://image.tmdb.org/t/p/w300_and_h450_bestv2/orV0.jpg);"></div>
  </div>
</div>
<div class="col-md-12"><div class="row movie-info"><div class="col-md-6">
<ul class="detail-page-info-list">
  <li><strong>Original name:</strong> 透明な夜に駆ける君と、目に見えない恋をした。</li>
  <li><strong>Type:</strong> Scripted</li>
  <li><strong>IMDb link:</strong> <a rel="nofollow" href="https://www.imdb.com/title/tt39304754/">39304754</a></li>
  <li><strong>IMDb rating:</strong> 8.7 (2,365 votes)</li>
</ul>
<img class="detail-torrent-image" src="/static/img/no-torrent-image.png" title="Some Series S01 - E12">
</div></div></div>
</body></html>`

// A series page must be identified as tv and must not fall back to the
// shared placeholder image.
func TestParseSeriesDetail(t *testing.T) {
	d := ParseDetail([]byte(seriesDetail))
	if d.Kind != "tv" {
		t.Errorf("Kind = %q, want tv", d.Kind)
	}
	if d.IMDbID != "tt39304754" {
		t.Errorf("IMDbID = %q, want tt39304754", d.IMDbID)
	}
	if d.PosterURL != "https://image.tmdb.org/t/p/w300_and_h450_bestv2/orV0.jpg" {
		t.Errorf("PosterURL = %q, want the tmdb background image", d.PosterURL)
	}
}

// A series page whose scraped metadata block is missing entirely -- the
// tracker publishes several of these -- has no label naming the media type.
// The poster block is the remaining marker, and without it the release name
// would be the only clue left.
const bareSeriesDetail = `<!doctype html><html><body>
<div class="poster-block">
  <div class="serial_poster__border1" style="background-image:url(https://static.tvmaze.com/uploads/images/original_untouched/572/1432197.jpg);"></div>
</div>
<div class="row movie-info"><ul class="detail-page-info-list">
  <li><strong>Torrent host:</strong> EXT</li>
  <li><strong>IMDb link:</strong> <a rel="nofollow" href="https://www.imdb.com/title/tt27776045/">27776045</a></li>
</ul>
<img class="detail-torrent-image" src="/static/img/no-torrent-image.png"></div>
</body></html>`

func TestParseBareSeriesDetail(t *testing.T) {
	d := ParseDetail([]byte(bareSeriesDetail))
	if d.Kind != "tv" {
		t.Errorf("Kind = %q, want tv from the poster block", d.Kind)
	}
	if d.IMDbID != "tt27776045" {
		t.Errorf("IMDbID = %q, want tt27776045", d.IMDbID)
	}
	if d.PosterURL != "https://static.tvmaze.com/uploads/images/original_untouched/572/1432197.jpg" {
		t.Errorf("PosterURL = %q, want the tvmaze artwork", d.PosterURL)
	}
}

// The fallback must not turn a film page into a series: the bare fixture has
// no "Movie:" row, so a film page keeps an empty kind and the forwarder falls
// back to the release-name prefix.
func TestParseInfoTitleWithoutSerialBlockStaysEmpty(t *testing.T) {
	page := `<html><body><ul><li><strong>Torrent host:</strong> EXT</li></ul>` +
		`<img class="detail-torrent-image" src="/static/img/no-torrent-image.png"></body></html>`
	if _, kind := parseInfoTitle([]byte(page)); kind != "" {
		t.Errorf("kind = %q, want empty when nothing identifies the media type", kind)
	}
}

// A page whose only image is the shared placeholder must yield no poster, so
// the forwarder posts text rather than the tracker's grey box.
func TestParsePlaceholderPosterIsIgnored(t *testing.T) {
	page := `<html><body><div class="movie-info"><ul class="detail-page-info-list">` +
		`<li><strong>Movie:</strong> <a href="/x/"><span>Some Film</span></a></li>` +
		`</ul><img class="detail-torrent-image" src="/static/img/no-torrent-image.png"></div></body></html>`
	d := ParseDetail([]byte(page))
	if d.PosterURL != "" {
		t.Errorf("PosterURL = %q, want empty for a placeholder", d.PosterURL)
	}
	if d.Kind != "movie" {
		t.Errorf("Kind = %q, want movie", d.Kind)
	}
}

func TestIsPlaceholderImage(t *testing.T) {
	placeholder := []string{
		"https://ext.to/static/img/no-torrent-image.png",
		"/static/img/no-image.png",
		"/static/img/placeholder.jpg",
	}
	for _, u := range placeholder {
		if !isPlaceholderImage(u) {
			t.Errorf("isPlaceholderImage(%q) = false, want true", u)
		}
	}
	real := []string{
		"https://ext.to/upload_files/torrents-imdb-posters/0da/x.jpg",
		"https://image.tmdb.org/t/p/w500/abc.jpg",
		"/static/img/source/eztv.png",
	}
	for _, u := range real {
		if isPlaceholderImage(u) {
			t.Errorf("isPlaceholderImage(%q) = true, want false", u)
		}
	}
}

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

// The captured series page confirms both differences at once: the media type
// comes from the scraped metadata block, and the artwork is a TMDB URL rather
// than the placeholder sitting in detail-torrent-image.
func TestParseLiveSeriesFixture(t *testing.T) {
	dir := os.Getenv("SCRAPE_FIXTURE_DIR")
	if dir == "" {
		t.Skip("set SCRAPE_FIXTURE_DIR to run against captured pages")
	}
	body, err := os.ReadFile(filepath.Join(dir, "detail_tv.html"))
	if err != nil {
		t.Skipf("no series capture available: %v", err)
	}
	d := ParseDetail(body)
	if d.Kind != "tv" {
		t.Errorf("kind = %q, want tv", d.Kind)
	}
	if d.IMDbID != "tt39304754" {
		t.Errorf("imdb = %q, want tt39304754", d.IMDbID)
	}
	if !strings.HasPrefix(d.PosterURL, "https://image.tmdb.org/t/p/") {
		t.Errorf("poster = %q, want a tmdb image", d.PosterURL)
	}
	if isPlaceholderImage(d.PosterURL) {
		t.Errorf("poster resolved to a placeholder: %q", d.PosterURL)
	}
}
