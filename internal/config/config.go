// Package config defines runtime settings for the ext.to forwarder.
//
// Settings are persisted as JSON so the web UI can edit them at runtime
// without restarting the container.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Category identifiers used by ext.to's browse endpoint (cat= query param).
const (
	CatMovies = 1
	CatTV     = 2
	CatMusic  = 3
	CatGames  = 4
	CatApps   = 5
	CatBooks  = 6
	CatAnime  = 7
	CatOther  = 8
	CatAll    = 9
)

// CategoryNames maps category ids to their display names.
var CategoryNames = map[int]string{
	CatMovies: "Movies",
	CatTV:     "TV Shows",
	CatMusic:  "Music",
	CatGames:  "Games",
	CatApps:   "Apps",
	CatBooks:  "Books",
	CatAnime:  "Anime",
	CatOther:  "Other",
	CatAll:    "All",
}

// Age windows accepted by the age= query param. Age 5+ is not a real window
// (ext.to redirects those to the advanced search page), so it is excluded.
var AgeNames = map[int]string{
	0: "Last 24 hours",
	1: "Last 3 days",
	2: "Last 7 days",
	3: "Last 14 days",
	4: "Last month",
}

// Store holds the mutable settings plus the file they are persisted to.
type Store struct {
	mu   sync.RWMutex
	path string
	cur  Settings
}

// Settings is the full configuration surface exposed to the web UI.
type Settings struct {
	// --- ext.to access -------------------------------------------------
	// Clearance is the cf_clearance cookie value harvested from a browser
	// that already passed the Cloudflare challenge. Cloudflare binds it to
	// the client IP, so it must be refreshed whenever the egress IP changes.
	Clearance string `json:"clearance"`
	// PHP session cookie, optional but improves fidelity.
	Session string `json:"session"`
	// UserAgent must match the browser that produced the clearance cookie.
	UserAgent string `json:"user_agent"`
	// Proxy is an optional HTTP(S) proxy, e.g. http://user:pass@host:port.
	Proxy string `json:"proxy"`

	// --- what to watch -------------------------------------------------
	Categories []int    `json:"categories"`
	Age        int      `json:"age"`
	Include    []string `json:"include"`
	Exclude    []string `json:"exclude"`
	MinSizeMB  float64  `json:"min_size_mb"`
	MaxSizeMB  float64  `json:"max_size_mb"`
	MaxPages   int      `json:"max_pages"`

	// --- how to deliver ------------------------------------------------
	BotToken     string `json:"bot_token"`
	ChatID       string `json:"chat_id"`
	MessageTopic string `json:"message_topic"`
	// Template controls the caption. See template.go for placeholders.
	Template   string `json:"template"`
	WithPoster bool   `json:"with_poster"`
	WithMagnet bool   `json:"with_magnet"`
	Silent     bool   `json:"silent"`
	DisableWeb bool   `json:"disable_web_preview"`

	// --- scheduling ----------------------------------------------------
	IntervalSeconds int  `json:"interval_seconds"`
	BatchSize       int  `json:"batch_size"`
	Enabled         bool `json:"enabled"`

	// --- web ui --------------------------------------------------------
	AdminUser     string `json:"admin_user"`
	AdminPassword string `json:"admin_password"`
}

// Default returns a settings value suitable for a fresh install: it watches
// every category in the last 24 hours and is disabled until the operator
// supplies Telegram credentials.
func Default() Settings {
	return Settings{
		UserAgent:       DefaultUserAgent,
		Categories:      []int{CatMovies, CatTV},
		Age:             0,
		MaxPages:        2,
		Template:        DefaultTemplate,
		WithPoster:      true,
		WithMagnet:      true,
		Silent:          false,
		IntervalSeconds: 600,
		BatchSize:       10,
		Enabled:         false,
		AdminUser:       "admin",
		AdminPassword:   "admin",
	}
}

// DefaultUserAgent mirrors the Chrome build used to harvest clearance
// cookies. Cloudflare compares it against the UA bound to cf_clearance.
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"

// Validate normalises and sanity-checks settings, returning a human readable
// error describing the first problem found.
func (s *Settings) Validate() error {
	if s.Age < 0 || s.Age > 4 {
		return fmt.Errorf("age window must be between 0 and 4")
	}
	if s.MaxPages < 1 {
		s.MaxPages = 1
	}
	if s.MaxPages > 20 {
		s.MaxPages = 20
	}
	if s.BatchSize < 1 {
		s.BatchSize = 1
	}
	if s.BatchSize > 50 {
		s.BatchSize = 50
	}
	if s.IntervalSeconds < 60 {
		s.IntervalSeconds = 60
	}
	if s.IntervalSeconds > 86400 {
		s.IntervalSeconds = 86400
	}
	if s.UserAgent == "" {
		s.UserAgent = DefaultUserAgent
	}
	if s.Template == "" {
		s.Template = DefaultTemplate
	}
	if len(s.Categories) == 0 {
		s.Categories = []int{CatAll}
	}
	for _, c := range s.Categories {
		if _, ok := CategoryNames[c]; !ok {
			return fmt.Errorf("unknown category id %d", c)
		}
	}
	for _, pat := range append(append([]string{}, s.Include...), s.Exclude...) {
		if pat == "" {
			continue
		}
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("invalid regular expression %q: %w", pat, err)
		}
	}
	if s.MinSizeMB < 0 || s.MaxSizeMB < 0 {
		return fmt.Errorf("size filters cannot be negative")
	}
	if s.MaxSizeMB > 0 && s.MinSizeMB > s.MaxSizeMB {
		return fmt.Errorf("minimum size is larger than maximum size")
	}
	if s.Enabled {
		if s.BotToken == "" {
			return fmt.Errorf("telegram bot token is required when monitoring is enabled")
		}
		if s.ChatID == "" {
			return fmt.Errorf("telegram chat id is required when monitoring is enabled")
		}
		if s.Clearance == "" {
			return fmt.Errorf("cf_clearance cookie is required when monitoring is enabled")
		}
	}
	return nil
}

// Masked returns a copy with secrets replaced, for display in the UI.
func (s Settings) Masked() Settings {
	c := s
	c.BotToken = mask(s.BotToken)
	c.Clearance = mask(s.Clearance)
	c.Session = mask(s.Session)
	c.AdminPassword = ""
	return c
}

func mask(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return strings.Repeat("*", len(v))
	}
	return v[:4] + strings.Repeat("*", 8) + v[len(v)-4:]
}

// Load reads settings from path, creating the file with defaults when absent.
func Load(path string) (*Store, error) {
	st := &Store{path: path, cur: Default()}
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if err := st.save(); err != nil {
			return nil, err
		}
		return st, nil
	case err != nil:
		return nil, err
	}
	// Start from defaults so newly added fields get sensible values.
	cur := Default()
	if err := json.Unmarshal(b, &cur); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	st.cur = cur
	return st, nil
}

// Get returns a copy of the current settings.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.cur
}

// Update validates and persists new settings.
func (st *Store) Update(next Settings) error {
	if err := next.Validate(); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.cur = next
	return st.save()
}

// save writes settings atomically. Callers must hold the lock (or be in Load).
func (st *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st.cur, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// Duration converts the polling interval to a time.Duration.
func (s Settings) Duration() time.Duration {
	return time.Duration(s.IntervalSeconds) * time.Second
}

// ParseSizeMB converts ext.to size strings such as "1.43 GB" or "662.42 MB"
// into megabytes. Unparseable input yields 0, false.
func ParseSizeMB(raw string) (float64, bool) {
	f := strings.Fields(strings.TrimSpace(raw))
	if len(f) != 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToUpper(f[1]) {
	case "B":
		return v / (1024 * 1024), true
	case "KB", "KIB":
		return v / 1024, true
	case "MB", "MIB":
		return v, true
	case "GB", "GIB":
		return v * 1024, true
	case "TB", "TIB":
		return v * 1024 * 1024, true
	default:
		return 0, false
	}
}
