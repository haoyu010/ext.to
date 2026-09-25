package scrape

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// challengeBody is what Cloudflare serves instead of the listing when the
// clearance cookie is missing or revoked.
const challengeBody = `<html><head><title>Just a moment...</title></head>` +
	`<body><span id="challenge-error-text">Enable JavaScript and cookies</span></body></html>`

// withBaseURL points the package at a test server for the duration of a test.
func withBaseURL(t *testing.T, url string) {
	t.Helper()
	restore := BaseURL
	BaseURL = url
	t.Cleanup(func() { BaseURL = restore })
}

func testClient(clearance string) *Client {
	return &Client{
		HTTP:      http.DefaultClient,
		Clearance: clearance,
		Session:   "session-old",
		UserAgent: "UA-browser",
	}
}

// TestGetRetriesOnceAfterRefresh is the core of the automatic cookie refresh:
// a challenged request triggers the hook, and the retry must then succeed.
func TestGetRetriesOnceAfterRefresh(t *testing.T) {
	var calls int32
	var secondCookie, secondUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The first request carries the stale cookie and is challenged; the
		// retry has to present the refreshed one.
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(challengeBody))
			return
		}
		secondCookie = r.Header.Get("Cookie")
		secondUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`<html><body><a class="torrent-title-link" href="/x-1/">t</a></body></html>`))
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	c := testClient("clearance-stale")
	var refreshes int32
	c.OnBlocked = func(context.Context) error {
		atomic.AddInt32(&refreshes, 1)
		c.Clearance = "clearance-fresh"
		c.Session = "session-fresh"
		c.UserAgent = "UA-solver"
		return nil
	}

	body, err := c.get(context.Background(), srv.URL+"/browse/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(string(body), "torrent-title-link") {
		t.Errorf("retry did not return the real page: %q", body)
	}
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Errorf("OnBlocked ran %d times, want exactly 1", got)
	}
	// A retry that replays the old credentials would fail identically, so the
	// refreshed cookie and User-Agent must be on the wire.
	if !strings.Contains(secondCookie, "cf_clearance=clearance-fresh") {
		t.Errorf("retry sent %q, want the refreshed clearance cookie", secondCookie)
	}
	if !strings.Contains(secondCookie, "PHPSESSID=session-fresh") {
		t.Errorf("retry sent %q, want the refreshed session cookie", secondCookie)
	}
	if secondUA != "UA-solver" {
		t.Errorf("retry User-Agent = %q, want UA-solver", secondUA)
	}
}

// TestGetGivesUpAfterOneRefresh stops a persistent block from looping: the
// cookie is only ever re-solved once per request.
func TestGetGivesUpAfterOneRefresh(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(challengeBody))
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	c := testClient("clearance-stale")
	var refreshes int32
	c.OnBlocked = func(context.Context) error {
		atomic.AddInt32(&refreshes, 1)
		return nil
	}

	_, err := c.get(context.Background(), srv.URL+"/browse/")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("made %d requests, want 2 (original plus one retry)", got)
	}
	if got := atomic.LoadInt32(&refreshes); got != 1 {
		t.Errorf("OnBlocked ran %d times, want 1", got)
	}
}

// TestGetWithoutHookReportsBlocked keeps the no-solver install behaving as it
// did before: an operator-facing error instead of a silent empty scan.
func TestGetWithoutHookReportsBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(challengeBody))
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	_, err := testClient("x").get(context.Background(), srv.URL+"/browse/")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

// TestGetSurfacesRefreshFailure checks that the reason the refresh failed wins
// over the block itself: "solver unreachable" is actionable, "Cloudflare
// blocked us" is not.
func TestGetSurfacesRefreshFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(challengeBody))
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	c := testClient("clearance-stale")
	c.OnBlocked = func(context.Context) error { return errors.New("FlareSolverr 未能通过验证") }

	_, err := c.get(context.Background(), srv.URL+"/browse/")
	if err == nil || !strings.Contains(err.Error(), "FlareSolverr") {
		t.Fatalf("err = %v, want the refresh failure", err)
	}
	if errors.Is(err, ErrBlocked) {
		t.Error("the refresh failure must not be reported as a plain Cloudflare block")
	}
}

// TestGetTreatsBare403AsBlocked covers the case the previous error message
// described: a 403 without the interstitial markers still means the cookie is
// no longer accepted, so it has to trigger the refresh too.
func TestGetTreatsBare403AsBlocked(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("<html><body>Forbidden</body></html>"))
			return
		}
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	c := testClient("clearance-stale")
	c.OnBlocked = func(context.Context) error { return nil }

	if _, err := c.get(context.Background(), srv.URL+"/browse/"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("made %d requests, want 2", got)
	}
}

// TestGetReportsOtherErrorsUnchanged ensures the retry path does not swallow
// ordinary server errors, which have nothing to do with the cookie.
func TestGetReportsOtherErrorsUnchanged(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	c := testClient("clearance-stale")
	c.OnBlocked = func(context.Context) error { return nil }

	_, err := c.get(context.Background(), srv.URL+"/browse/")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want an HTTP 500 error", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("made %d requests, want 1 (a 500 is not a cookie problem)", got)
	}
}

// TestMagnetFromDetailRefetchesAfterRefresh pins the signing contract: the
// magnet form is signed with the page token and the CSRF token from a specific
// page load, so replaying the same form with a new session cannot work. The
// detail page must be read again.
func TestMagnetFromDetailRefetchesAfterRefresh(t *testing.T) {
	detail := readFixture(t, "detail_movie.html")
	token, csrf := parseTokens(detail)
	if token == "" || csrf == "" {
		t.Fatal("the detail fixture must carry a page token and a csrf token")
	}

	var detailCalls, magnetCalls int32
	var magnetForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/ajax/getTorrentMagnet.php"):
			if atomic.AddInt32(&magnetCalls, 1) == 1 {
				// First attempt: the session behind the signed form is stale.
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(challengeBody))
				return
			}
			_ = r.ParseForm()
			magnetForm = r.Form.Get("sessid")
			_, _ = w.Write([]byte(`{"success":true,"url":"magnet:?xt=urn:btih:abc"}`))
		default:
			atomic.AddInt32(&detailCalls, 1)
			_, _ = w.Write(detail)
		}
	}))
	defer srv.Close()
	withBaseURL(t, srv.URL)

	c := testClient("clearance-stale")
	var refreshes int32
	c.OnBlocked = func(context.Context) error {
		atomic.AddInt32(&refreshes, 1)
		return nil
	}
	item := Item{ID: 22395007, Slug: "detail_movie", URL: srv.URL + "/detail_movie-22395007/"}

	got, err := c.MagnetFromDetail(context.Background(), item, detail)
	if err != nil {
		t.Fatalf("MagnetFromDetail: %v", err)
	}
	if got != "magnet:?xt=urn:btih:abc" {
		t.Errorf("magnet = %q", got)
	}
	if n := atomic.LoadInt32(&refreshes); n != 1 {
		t.Errorf("OnBlocked ran %d times, want 1", n)
	}
	// The caller supplied the first body, so the only detail fetch is the one
	// made after the refresh. A zero here would mean the stale page was
	// reused, whose token and csrf are bound to the session that was rejected.
	if n := atomic.LoadInt32(&detailCalls); n != 1 {
		t.Errorf("detail page fetched %d times, want 1 refetch after the refresh", n)
	}
	if n := atomic.LoadInt32(&magnetCalls); n != 2 {
		t.Errorf("magnet endpoint called %d times, want 2", n)
	}
	if magnetForm != csrf {
		t.Errorf("sessid = %q, want the csrf token %q", magnetForm, csrf)
	}
}

// TestListURLPointsAtTheScannedPage guards the URL the solver is handed: a
// cookie solved for one path is challenged again on another, so the target has
// to be the very page FetchList requests.
func TestListURLPointsAtTheScannedPage(t *testing.T) {
	got := ListURL(7, 0, 1)
	want := BaseURL + listPath + "&age=0&cat=7&page=1"
	if got != want {
		t.Errorf("ListURL = %q, want %q", got, want)
	}
}
