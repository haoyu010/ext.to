package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haoyu010/ext.to/internal/config"
)

// TestSolveCookieWithoutSolverExplainsItself covers the panel button on an
// install that has no sidecar: the answer has to say what to add, because the
// operator pressed a button expecting it to work.
func TestSolveCookieWithoutSolverExplainsItself(t *testing.T) {
	s := newTestServer(t, func(st *config.Settings) {
		st.SolverURL = ""
		st.AutoRefreshClearance = false
	})
	rec := httptest.NewRecorder()
	s.handleSolveCookie(rec, httptest.NewRequest(http.MethodPost, "/api/solve-cookie", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with ok:false", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["ok"] != false {
		t.Errorf("ok = %v, want false", got["ok"])
	}
	msg, _ := got["error"].(string)
	if !strings.Contains(msg, "flaresolverr") {
		t.Errorf("error = %q, want it to name the address to configure", msg)
	}
}

// TestSolveCookieReturnsMaskedSettings ensures the refreshed cookie is never
// echoed in full: the panel needs to redraw the form, not learn the secret.
func TestSolveCookieReturnsMaskedSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"userAgent":"UA",` +
			`"cookies":[{"name":"cf_clearance","value":"a-very-long-clearance-value-here"}]}}`))
	}))
	defer srv.Close()

	s := newTestServer(t, func(st *config.Settings) {
		st.SolverURL = srv.URL
		st.AutoRefreshClearance = true
	})
	rec := httptest.NewRecorder()
	s.handleSolveCookie(rec, httptest.NewRequest(http.MethodPost, "/api/solve-cookie", nil))

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v, error = %v", got["ok"], got["error"])
	}
	settings, ok := got["settings"].(map[string]any)
	if !ok {
		t.Fatalf("settings missing from the response: %v", got)
	}
	if secret, _ := settings["clearance"].(string); strings.Contains(secret, "a-very-long-clearance-value-here") {
		t.Errorf("the response leaked the raw cookie: %q", secret)
	}
	// The stored value is what the next request uses, so it must be the real
	// one even though the response only carries the mask.
	if stored := s.cfg.Get().Clearance; stored != "a-very-long-clearance-value-here" {
		t.Errorf("stored clearance = %q, want the solved value", stored)
	}
}
