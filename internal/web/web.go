// Package web serves the configuration UI and JSON API.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/forwarder"
	"github.com/haoyu010/ext.to/internal/store"
)

// Server exposes the dashboard and its backing API.
type Server struct {
	cfg   *config.Store
	state *store.Store
	fwd   *forwarder.Forwarder
	log   *log.Logger

	sessions *sessionStore
	version  string
	LogRing  *LogRing
}

// Options configures a Server.
type Options struct {
	Config  *config.Store
	Store   *store.Store
	Forward *forwarder.Forwarder
	Logger  *log.Logger
	Version string
	// LogRing receives a copy of every log line for display in the dashboard.
	// When nil, log viewing is disabled.
	LogRing *LogRing
}

// New builds a Server.
func New(o Options) *Server {
	logger := o.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		cfg:      o.Config,
		state:    o.Store,
		fwd:      o.Forward,
		log:      logger,
		sessions: newSessionStore(12 * time.Hour),
		version:  o.Version,
		LogRing:  o.LogRing,
	}
}

// Routes wires the HTTP handlers.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	mux.HandleFunc("GET /api/state", s.auth(s.handleState))
	mux.HandleFunc("GET /api/settings", s.auth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.auth(s.handlePutSettings))
	mux.HandleFunc("GET /api/meta", s.auth(s.handleMeta))
	mux.HandleFunc("GET /api/logs", s.auth(s.handleLogs))
	mux.HandleFunc("GET /api/records", s.auth(s.handleRecords))

	mux.HandleFunc("POST /api/check", s.auth(s.handleCheck))
	mux.HandleFunc("POST /api/test", s.auth(s.handleTest))
	mux.HandleFunc("POST /api/test-telegram", s.auth(s.handleTestTelegram))
	mux.HandleFunc("POST /api/start", s.auth(s.handleStart))
	mux.HandleFunc("POST /api/stop", s.auth(s.handleStop))
	mux.HandleFunc("POST /api/clear", s.auth(s.handleClear))
	mux.HandleFunc("GET /api/health", s.handleHealth)

	return logRequests(s.log, mux)
}

// ---------------------------------------------------------------- auth ----

type sessionStore struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, m: map[string]time.Time{}}
}

func (s *sessionStore) create() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.m[tok] = time.Now().Add(s.ttl)
	return tok, nil
}

func (s *sessionStore) valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	exp, ok := s.m[tok]
	if !ok || time.Now().After(exp) {
		delete(s.m, tok)
		return false
	}
	// Sliding expiry keeps an actively used dashboard logged in.
	s.m[tok] = time.Now().Add(s.ttl)
	return true
}

func (s *sessionStore) drop(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tok)
}

func (s *sessionStore) gcLocked() {
	now := time.Now()
	for k, exp := range s.m {
		if now.After(exp) {
			delete(s.m, k)
		}
	}
}

const sessionCookie = "extto_session"

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.sessions.valid(c.Value) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "not authenticated",
			})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed request body"})
		return
	}
	cur := s.cfg.Get()
	// Constant-time comparison avoids leaking credential length or content.
	userOK := subtle.ConstantTimeCompare([]byte(body.User), []byte(cur.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(body.Password), []byte(cur.AdminPassword)) == 1
	if !userOK || !passOK {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}
	tok, err := s.sessions.create()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "cannot create session"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((12 * time.Hour).Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --------------------------------------------------------------- state ----

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": s.version,
		"running": s.fwd.Running(),
	})
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	stats := s.state.Stats()
	last := s.fwd.LastRun()
	writeJSON(w, http.StatusOK, map[string]any{
		"running":    s.fwd.Running(),
		"stats":      stats,
		"last_run":   last,
		"version":    s.version,
		"server_now": time.Now(),
		"next_run":   nextRun(s.fwd.Running(), s.cfg.Get().Duration(), last.EndedAt),
	})
}

// nextRun estimates when the next scan starts so the dashboard can show a
// countdown. It returns nil when monitoring is stopped.
func nextRun(running bool, interval time.Duration, lastEnd time.Time) *time.Time {
	if !running || lastEnd.IsZero() {
		return nil
	}
	t := lastEnd.Add(interval)
	return &t
}

var passwordPlaceholder = "__unchanged__"

func (s *Server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	cur := s.cfg.Get()
	// The UI needs the label maps and template help alongside the values.
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": cur.Masked(),
		"meta":     metaPayload(),
	})
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, metaPayload())
}

func metaPayload() map[string]any {
	cats := make([]map[string]any, 0, len(config.CategoryNames))
	for i := 1; i <= 9; i++ {
		if name, ok := config.CategoryNames[i]; ok {
			cats = append(cats, map[string]any{"id": i, "name": name})
		}
	}
	ages := make([]map[string]any, 0, len(config.AgeNames))
	for i := 0; i <= 4; i++ {
		ages = append(ages, map[string]any{"id": i, "name": config.AgeNames[i]})
	}
	fields := make([]map[string]string, 0, len(config.TemplateFields))
	for _, f := range config.TemplateFields {
		fields = append(fields, map[string]string{"key": f.Key, "desc": f.Desc})
	}
	return map[string]any{
		"categories":       cats,
		"ages":             ages,
		"template_fields":  fields,
		"default_template": config.DefaultTemplate,
	}
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var incoming config.Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&incoming); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed request body"})
		return
	}
	cur := s.cfg.Get()

	// Secrets arrive masked. Preserve the stored value unless the operator
	// typed a replacement, and treat the placeholder as "keep".
	incoming.BotToken = resolveSecret(incoming.BotToken, cur.BotToken)
	incoming.Clearance = resolveSecret(incoming.Clearance, cur.Clearance)
	incoming.Session = resolveSecret(incoming.Session, cur.Session)
	if incoming.AdminPassword == "" || incoming.AdminPassword == passwordPlaceholder {
		incoming.AdminPassword = cur.AdminPassword
	}
	if incoming.AdminUser == "" {
		incoming.AdminUser = cur.AdminUser
	}

	if err := s.cfg.Update(incoming); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.log.Printf("web: settings updated")

	// Apply the new monitoring state immediately.
	if next := s.cfg.Get(); next.Enabled {
		s.fwd.Start()
	} else {
		s.fwd.Stop()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"settings": s.cfg.Get().Masked(),
	})
}

// resolveSecret keeps the stored value when the incoming field is empty, the
// mask sent by the UI, or the explicit placeholder.
func resolveSecret(incoming, stored string) string {
	switch {
	case incoming == "":
		return stored
	case incoming == passwordPlaceholder:
		return stored
	case strings.Contains(incoming, "********"):
		return stored
	default:
		return strings.TrimSpace(incoming)
	}
}

func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "sent", "pending", "failed":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid status filter"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"records": s.state.Recent(limit, status),
	})
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	rep, err := s.fwd.ForceCheck(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"report": rep,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "report": rep})
}

func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	sample := 25
	if v := r.URL.Query().Get("sample"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			sample = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	res, err := s.fwd.Test(ctx, sample)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func (s *Server) handleTestTelegram(w http.ResponseWriter, r *http.Request) {
	// Test the values currently on screen, falling back to stored secrets.
	var body config.Settings
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	}
	cur := s.cfg.Get()
	body.BotToken = resolveSecret(body.BotToken, cur.BotToken)
	body.Clearance = resolveSecret(body.Clearance, cur.Clearance)
	body.Session = resolveSecret(body.Session, cur.Session)
	if body.ChatID == "" {
		body.ChatID = cur.ChatID
	}
	if body.MessageTopic == "" {
		body.MessageTopic = cur.MessageTopic
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	msg, err := s.fwd.TestTelegram(ctx, body)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "detail": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": msg})
}

func (s *Server) handleStart(w http.ResponseWriter, _ *http.Request) {
	cur := s.cfg.Get()
	if err := cur.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !cur.Enabled {
		cur.Enabled = true
		if err := s.cfg.Update(cur); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	s.fwd.Start()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": s.fwd.Running()})
}

func (s *Server) handleStop(w http.ResponseWriter, _ *http.Request) {
	cur := s.cfg.Get()
	if cur.Enabled {
		cur.Enabled = false
		if err := s.cfg.Update(cur); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	s.fwd.Stop()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": s.fwd.Running()})
}

func (s *Server) handleClear(w http.ResponseWriter, _ *http.Request) {
	s.state.Clear()
	if err := s.state.Flush(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	s.log.Printf("web: history cleared; the next scan will rebuild a baseline")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLogs streams the in-memory log buffer.
func (s *Server) handleLogs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"lines": s.LogRing.Snapshot()})
}

// ------------------------------------------------------------- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		// The response is already partially written; nothing useful to do.
		_ = err
	}
}

func logRequests(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 400 {
			logger.Printf("http: %s %s -> %d (%s)", r.Method, r.URL.Path,
				rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
