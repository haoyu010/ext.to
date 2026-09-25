package forwarder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/scrape"
)

// fakeSolver serves the FlareSolverr v1 shape with a fresh cookie. The caller
// appends /v1 to the returned server URL, matching how an operator configures
// the endpoint.
func fakeSolver(t *testing.T, clearance, session, ua string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1" {
			t.Errorf("solver called at %q, want /v1", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		body := `{"status":"ok","message":"Challenge solved!","solution":{` +
			`"userAgent":"` + ua + `","cookies":[` +
			`{"name":"cf_clearance","value":"` + clearance + `"},` +
			`{"name":"PHPSESSID","value":"` + session + `"}]}}`
		_, _ = w.Write([]byte(body))
	}))
}

// TestSolveStoresCookieAndUserAgent covers the manual refresh: both values have
// to be persisted, because a cookie without the matching User-Agent is rejected
// by Cloudflare on the very next request.
func TestSolveStoresCookieAndUserAgent(t *testing.T) {
	srv := fakeSolver(t, "fresh-clearance", "fresh-session", "UA-from-solver")
	defer srv.Close()

	f, _, _ := newTestForwarder(t, func(s *config.Settings) {
		s.SolverURL = srv.URL + "/v1"
		s.Clearance = "stale-clearance"
		s.UserAgent = "UA-old"
	})

	sol, err := f.Solve(context.Background())
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if sol.Clearance != "fresh-clearance" {
		t.Errorf("returned clearance = %q", sol.Clearance)
	}

	got := f.cfg.Get()
	if got.Clearance != "fresh-clearance" {
		t.Errorf("stored clearance = %q, want fresh-clearance", got.Clearance)
	}
	if got.Session != "fresh-session" {
		t.Errorf("stored session = %q, want fresh-session", got.Session)
	}
	// The User-Agent is half of the binding, so leaving the old one stored
	// would produce a cookie that fails immediately.
	if got.UserAgent != "UA-from-solver" {
		t.Errorf("stored user agent = %q, want UA-from-solver", got.UserAgent)
	}
}

// TestSolveSolvesTheScannedPage pins the target URL: a clearance is issued for
// the interstitial Cloudflare served, so solving the site root would leave the
// listing path challenged again.
func TestSolveSolvesTheScannedPage(t *testing.T) {
	var target string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Cmd string `json:"cmd"`
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode solver request: %v", err)
		}
		target = body.URL
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"userAgent":"UA",` +
			`"cookies":[{"name":"cf_clearance","value":"c"}]}}`))
	}))
	defer srv.Close()

	f, _, _ := newTestForwarder(t, func(s *config.Settings) {
		s.SolverURL = srv.URL
		s.Categories = []int{config.CatAnime}
		s.Age = 2
	})

	if _, err := f.Solve(context.Background()); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := scrape.ListURL(config.CatAnime, 2, 1)
	if target != want {
		t.Errorf("solved %q, want the scanned listing page %q", target, want)
	}
}

// TestSolveWithoutSolverIsReported keeps the no-sidecar install honest: the
// panel button must say what is missing instead of failing silently.
func TestSolveWithoutSolverIsReported(t *testing.T) {
	f, _, _ := newTestForwarder(t, func(s *config.Settings) {
		s.SolverURL = ""
		s.AutoRefreshClearance = false
	})
	_, err := f.Solve(context.Background())
	if err == nil {
		t.Fatal("expected an error when no solver is configured")
	}
	if !strings.Contains(err.Error(), "FlareSolverr") {
		t.Errorf("err = %v, want it to name the missing solver", err)
	}
}

// TestSolveFailureLeavesSettingsAlone guards the cookie already in use: a
// failed solve must not wipe working credentials, or turning the feature on
// would be able to break a running install.
func TestSolveFailureLeavesSettingsAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","message":"Challenge not detected!","solution":{}}`))
	}))
	defer srv.Close()

	f, _, _ := newTestForwarder(t, func(s *config.Settings) {
		s.SolverURL = srv.URL
		s.Clearance = "working-clearance"
		s.Session = "working-session"
		s.UserAgent = "UA-working"
	})

	if _, err := f.Solve(context.Background()); err == nil {
		t.Fatal("expected an error when the solver fails")
	}
	got := f.cfg.Get()
	if got.Clearance != "working-clearance" || got.Session != "working-session" ||
		got.UserAgent != "UA-working" {
		t.Errorf("a failed solve must not change the stored credentials, got %+v", got)
	}
}

// TestRefreshHookCountsOnTheReport makes the automatic refresh visible: without
// the counter an operator cannot tell "the scan worked" from "the scan worked
// only because the cookie was re-solved".
func TestRefreshHookCountsOnTheReport(t *testing.T) {
	srv := fakeSolver(t, "fresh", "session", "UA")
	defer srv.Close()

	f, _, _ := newTestForwarder(t, func(s *config.Settings) {
		s.SolverURL = srv.URL + "/v1"
		s.Clearance = "stale"
	})

	var rep RunReport
	hook := f.refreshClearance(testClient(f), &rep)
	if err := hook(context.Background()); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if rep.Refreshed != 1 {
		t.Errorf("Refreshed = %d, want 1", rep.Refreshed)
	}
}

// TestRefreshHookIsOffWhenDisabled keeps the switch meaningful: an install
// without a solver must get the plain block error, not a solve attempt.
func TestRefreshHookIsOffWhenDisabled(t *testing.T) {
	srv := fakeSolver(t, "fresh", "session", "UA")
	defer srv.Close()

	f, _, _ := newTestForwarder(t, func(s *config.Settings) {
		s.SolverURL = srv.URL
		s.AutoRefreshClearance = false
	})

	hook := f.refreshClearance(testClient(f), nil)
	err := hook(context.Background())
	if err == nil {
		t.Fatal("expected an error when the automatic refresh is switched off")
	}
	if !strings.Contains(err.Error(), "关闭") {
		t.Errorf("err = %v, want it to say the refresh is off", err)
	}
}

// testClient builds the scrape client the hook is expected to update.
func testClient(f *Forwarder) *scrape.Client {
	s := f.cfg.Get()
	return &scrape.Client{HTTP: http.DefaultClient, Clearance: s.Clearance, UserAgent: s.UserAgent}
}
