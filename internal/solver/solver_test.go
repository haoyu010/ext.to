package solver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// flareResponse is the shape FlareSolverr returns on success.
const flareResponse = `{
  "status": "ok",
  "message": "Challenge solved!",
  "solution": {
    "response": "<html>listing</html>",
    "userAgent": "Mozilla/5.0 (X11; Linux x86_64) Chrome/152.0.0.0",
    "cookies": [
      {"name": "cf_clearance", "value": "clearance-value", "domain": ".ext.to", "path": "/"},
      {"name": "PHPSESSID", "value": "session-value", "domain": ".ext.to", "path": "/"}
    ]
  }
}`

func TestSolveParsesBundle(t *testing.T) {
	var gotPath string
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("solver must be called with POST, got %s", r.Method)
		}
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(flareResponse))
	}))
	defer srv.Close()

	target := "https://ext.to/browse/?cat=1&sort=age&order=desc&age=0&cat=1&page=1"
	sol, err := New(srv.URL+"/v1").Solve(context.Background(), target)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if sol.Clearance != "clearance-value" {
		t.Errorf("Clearance = %q, want clearance-value", sol.Clearance)
	}
	if sol.Session != "session-value" {
		t.Errorf("Session = %q, want session-value", sol.Session)
	}
	if !strings.Contains(sol.UserAgent, "Chrome/152") {
		t.Errorf("UserAgent = %q, want the browser identity from the solution", sol.UserAgent)
	}
	if len(sol.Cookies) != 2 {
		t.Errorf("Cookies len = %d, want 2", len(sol.Cookies))
	}
	if gotPath != "/v1" {
		t.Errorf("posted to %q, want /v1", gotPath)
	}
	// The URL under test is what gets solved; solving a different page would
	// produce a cookie Cloudflare challenges again on the real one.
	if gotBody.URL != target {
		t.Errorf("solved %q, want %q", gotBody.URL, target)
	}
	if gotBody.Cmd != "request.get" {
		t.Errorf("cmd = %q, want request.get", gotBody.Cmd)
	}
	if gotBody.MaxTimeout == 0 {
		t.Error("maxTimeout must be set, or the solver gives up before the challenge resolves")
	}
}

func TestSolveWithoutClearanceIsAnError(t *testing.T) {
	// status ok but no cf_clearance happens when the solver's browser still
	// holds a valid cookie and no challenge was served. There is nothing to
	// store, so it must not be reported as a success.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"userAgent":"UA","cookies":[]}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL).Solve(context.Background(), "https://ext.to/")
	if err == nil {
		t.Fatal("expected an error when the solution carries no cf_clearance")
	}
	if !strings.Contains(err.Error(), "cf_clearance") {
		t.Errorf("error = %v, want it to name the missing cookie", err)
	}
}

func TestSolveReportsSolverFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","message":"Challenge not detected!","solution":{}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL).Solve(context.Background(), "https://ext.to/")
	if err == nil {
		t.Fatal("expected an error when the solver reports failure")
	}
	// The solver's own wording is what tells an operator whether the site is
	// down or the challenge changed, so it has to survive into the message.
	if !strings.Contains(err.Error(), "Challenge not detected") {
		t.Errorf("error = %v, want the solver's message", err)
	}
}

func TestSolveGarbageResponseIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer srv.Close()

	_, err := New(srv.URL).Solve(context.Background(), "https://ext.to/")
	if err == nil {
		t.Fatal("expected an error for a non-JSON response")
	}
	if !strings.Contains(err.Error(), "无法解析") {
		t.Errorf("error = %v, want it to say the response could not be parsed", err)
	}
}

func TestSolveWithoutEndpoint(t *testing.T) {
	if _, err := New("  ").Solve(context.Background(), "https://ext.to/"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
	if New("").Configured() {
		t.Error("Configured must be false for an empty endpoint")
	}
}

// TestSolveUnreachableSolverNamesTheAddress keeps the connection failure
// actionable: the address is the thing the operator typed, so it belongs in the
// message rather than a bare "connection refused".
func TestSolveUnreachableSolverNamesTheAddress(t *testing.T) {
	c := New("http://127.0.0.1:1/v1")
	_, err := c.Solve(context.Background(), "https://ext.to/")
	if err == nil {
		t.Fatal("expected an error for an unreachable solver")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error = %v, want it to include the configured address", err)
	}
}
