// Package config defines runtime settings for the ext.to forwarder.
//
// Settings are persisted as JSON so the web UI can edit them at runtime
// without restarting the container.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haoyu010/ext.to/internal/rules"
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

// CategoryNamesZH maps category ids to Chinese display names for the
// dashboard. The forwarder only ever uses CategoryNames for log lines, so the
// two maps stay independent.
var CategoryNamesZH = map[int]string{
	CatMovies: "电影",
	CatTV:     "剧集",
	CatMusic:  "音乐",
	CatGames:  "游戏",
	CatApps:   "软件",
	CatBooks:  "图书",
	CatAnime:  "动漫",
	CatOther:  "其他",
	CatAll:    "全部",
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

// AgeNamesZH maps age windows to Chinese display names for the dashboard.
var AgeNamesZH = map[int]string{
	0: "最近 24 小时",
	1: "最近 3 天",
	2: "最近 7 天",
	3: "最近 14 天",
	4: "最近 1 个月",
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
	// SolverURL points at a FlareSolverr v1 endpoint, for example
	// http://flaresolverr:8191/v1. When set, a rejected cookie is refreshed
	// automatically instead of stopping the loop until someone pastes a new
	// one by hand.
	SolverURL string `json:"solver_url"`
	// AutoRefreshClearance enables that refresh. It is a separate switch
	// because the solver is an extra container: an install without one must
	// not spend every cycle retrying a connection that cannot succeed.
	AutoRefreshClearance bool `json:"auto_refresh_clearance"`

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

	// --- tmdb enrichment ------------------------------------------------
	// TMDBKey enables matching posts against The Movie Database. An empty
	// key disables enrichment entirely.
	TMDBKey string `json:"tmdb_key"`
	// TMDBLang sets the language for titles and overviews, for example
	// "zh-CN" or "en-US".
	TMDBLang string `json:"tmdb_lang"`
	// TMDBOnly keeps the forwarder from posting torrents with no TMDB match,
	// so the channel only ever receives verified movie and TV releases.
	TMDBOnly bool `json:"tmdb_only"`
	// PosterSource picks where the poster image comes from: "auto" prefers
	// the TMDB artwork and falls back to the tracker image, "tmdb" and
	// "tracker" force one source.
	PosterSource string `json:"poster_source"`
	// --- classification -------------------------------------------------
	// CategoryRules is a YAML rule set that names a category from the TMDB
	// metadata of a match. Empty disables classification, which leaves the
	// blacklists below with nothing to act on.
	CategoryRules string `json:"category_rules"`
	// CategoryBlacklist drops releases whose classified category is listed.
	// The tracker's own category is far too coarse to tell a Chinese cartoon
	// from a Japanese one, so the exclusion has to happen after the TMDB
	// match, on the classified name.
	CategoryBlacklist []string `json:"category_blacklist"`
	// GenreBlacklist drops releases by TMDB genre name, for example 真人秀.
	// Names are matched case-insensitively against the localised names.
	GenreBlacklist []string `json:"genre_blacklist"`
	// GenreIDBlacklist drops releases by TMDB genre id, which is stable
	// across languages and therefore safer than a translated name.
	GenreIDBlacklist []int `json:"genre_id_blacklist"`

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
		UserAgent: DefaultUserAgent,
		// A fresh install runs behind the bundled FlareSolverr sidecar, so the
		// cookie refresh is on by default: without it the operator has to
		// notice a 403 in the log and paste a browser cookie by hand, which is
		// exactly the failure this default removes.
		SolverURL:            DefaultSolverURL,
		AutoRefreshClearance: true,
		// The tracker files Chinese animation under 动漫, not under 剧集, so
		// 剧集 alone cannot deliver the 国漫 the default rules are written to
		// keep. The shipped defaults have to be coherent with each other, or a
		// fresh install looks like a broken filter instead of a wrong category
		// selection. Movies are left out because no default rule keeps them.
		Categories:      []int{CatTV, CatAnime},
		Age:             0,
		MaxPages:        2,
		Template:        DefaultTemplate,
		WithPoster:      true,
		WithMagnet:      true,
		Silent:          false,
		IntervalSeconds: 600,
		BatchSize:       10,
		Enabled:         false,
		TMDBLang:        "zh-CN",
		TMDBOnly:        false,
		PosterSource:    PosterSourceAuto,
		// Classification is on by default: a fresh install follows Chinese
		// animation and Chinese TV rather than everything the tracker lists,
		// which is what the rule set and the exclusion list together express.
		CategoryRules:     DefaultCategoryRules,
		CategoryBlacklist: append([]string(nil), DefaultCategoryBlacklist...),
		GenreBlacklist:    append([]string(nil), DefaultGenreBlacklist...),
		GenreIDBlacklist:  append([]int(nil), DefaultGenreIDBlacklist...),
		AdminUser:         "admin",
		AdminPassword:     "admin",
	}
}

// Poster image sources accepted by Settings.PosterSource.
const (
	PosterSourceAuto    = "auto"
	PosterSourceTMDB    = "tmdb"
	PosterSourceTracker = "tracker"
)

// PosterSources lists the valid PosterSource values for the UI.
var PosterSources = []string{PosterSourceAuto, PosterSourceTMDB, PosterSourceTracker}

// DefaultUserAgent mirrors the Chrome build used to harvest clearance
// cookies. Cloudflare compares it against the UA bound to cf_clearance.
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"

// DefaultSolverURL is the address of the FlareSolverr sidecar in the shipped
// compose file. The service name resolves on the compose network, so it needs
// no host configuration.
const DefaultSolverURL = "http://flaresolverr:8191/v1"

// Validate normalises and sanity-checks settings, returning a human readable
// error describing the first problem found.
func (s *Settings) Validate() error {
	if s.Age < 0 || s.Age > 4 {
		return fmt.Errorf("时间范围必须在 0 到 4 之间")
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
	s.SolverURL = strings.TrimSpace(s.SolverURL)
	if s.SolverURL != "" {
		u, err := url.Parse(s.SolverURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("FlareSolverr 地址 %q 无效，应形如 http://flaresolverr:8191/v1", s.SolverURL)
		}
	}
	// Leaving the switch on without an address would make every failed scan
	// report a refresh it can never perform, so the two are kept consistent.
	if s.AutoRefreshClearance && s.SolverURL == "" {
		s.AutoRefreshClearance = false
	}
	if s.Template == "" {
		s.Template = DefaultTemplate
	}
	s.Template = migrateLegacyTemplate(s.Template)
	if s.TMDBLang == "" {
		s.TMDBLang = "zh-CN"
	}
	if s.PosterSource == "" {
		s.PosterSource = PosterSourceAuto
	}
	switch s.PosterSource {
	case PosterSourceAuto, PosterSourceTMDB, PosterSourceTracker:
	default:
		return fmt.Errorf("海报来源只能是 %s、%s 或 %s",
			PosterSourceAuto, PosterSourceTMDB, PosterSourceTracker)
	}
	if s.PosterSource != PosterSourceTracker && s.TMDBOnly && s.TMDBKey == "" {
		return fmt.Errorf("勾选「只推送匹配到 TMDB 的种子」时必须填写 TMDB API Key")
	}
	if len(s.Categories) == 0 {
		s.Categories = []int{CatAll}
	}
	for _, c := range s.Categories {
		if _, ok := CategoryNames[c]; !ok {
			return fmt.Errorf("未知的分类编号 %d", c)
		}
	}
	for _, pat := range append(append([]string{}, s.Include...), s.Exclude...) {
		if pat == "" {
			continue
		}
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("正则表达式 %q 无效：%w", pat, err)
		}
	}
	if s.MinSizeMB < 0 || s.MaxSizeMB < 0 {
		return fmt.Errorf("体积过滤不能为负数")
	}
	if s.MaxSizeMB > 0 && s.MinSizeMB > s.MaxSizeMB {
		return fmt.Errorf("最小体积大于最大体积")
	}
	// A malformed rule file must be rejected when it is saved. Catching it
	// during a scan instead would surface as a failed cycle with the reason
	// buried in the log.
	if strings.TrimSpace(s.CategoryRules) != "" {
		if _, err := rules.Parse([]byte(s.CategoryRules)); err != nil {
			return fmt.Errorf("分类规则无法解析：%w", err)
		}
	}
	for _, id := range s.GenreIDBlacklist {
		if id <= 0 {
			return fmt.Errorf("Genre ID 必须是正整数，收到 %d", id)
		}
	}
	// A category blacklist entry that matches no rule can never fire, and the
	// usual cause is a typo or a stale name left over after editing the rules.
	// Silently ignoring it would leave the operator believing a category is
	// excluded when it is not.
	if len(s.CategoryBlacklist) > 0 && strings.TrimSpace(s.CategoryRules) != "" {
		if err := checkBlacklistNames(s.CategoryRules, s.CategoryBlacklist); err != nil {
			return err
		}
	}
	// Dropping every category would silently stop all forwarding, so an
	// accidental all-blacklist entry is reported rather than obeyed.
	if len(s.CategoryBlacklist) > 0 && strings.TrimSpace(s.CategoryRules) == "" {
		return fmt.Errorf("填写了分类黑名单，但没有分类规则可用；请先配置分类规则")
	}
	if s.Enabled {
		if s.BotToken == "" {
			return fmt.Errorf("开启监听前请先填写机器人 Token")
		}
		if s.ChatID == "" {
			return fmt.Errorf("开启监听前请先填写 Chat ID")
		}
		if s.Clearance == "" {
			// With a solver configured the cookie is obtained on demand, and
			// the first scan refreshes it before the listing is read. Demanding
			// one by hand would defeat the point of the sidecar.
			if !(s.AutoRefreshClearance && s.SolverURL != "") {
				return fmt.Errorf("开启监听前请先填写 cf_clearance Cookie，" +
					"或配置 FlareSolverr 地址并开启自动刷新")
			}
		}
	}
	return nil
}

// checkBlacklistNames reports names on the blacklist that no rule defines, so a
// silently ineffective exclusion is caught when it is saved.
func checkBlacklistNames(ruleYAML string, blacklist []string) error {
	c, err := rules.Parse([]byte(ruleYAML))
	if err != nil {
		// The rule document is validated separately; a parse failure here
		// would be reported twice.
		return nil
	}
	known := map[string]bool{}
	for _, n := range c.Names() {
		known[n] = true
	}
	// 未分类 is produced whenever a release is matched but named by no rule,
	// so it is a valid entry even when the rule set has no catch-all for it.
	known[rules.Unclassified] = true
	var unknown []string
	for _, name := range blacklist {
		name = strings.TrimSpace(name)
		if name != "" && !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("分类黑名单里的 %s 在分类规则中不存在，请核对名称或补上对应规则",
			strings.Join(unknown, "、"))
	}
	return nil
}

// Masked returns a copy with secrets replaced, for display in the UI.
func (s Settings) Masked() Settings {
	c := s
	c.BotToken = mask(s.BotToken)
	c.Clearance = mask(s.Clearance)
	c.Session = mask(s.Session)
	c.TMDBKey = mask(s.TMDBKey)
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
	// An install that never edited its caption still carries a preset from an
	// earlier release, so the migration runs at load rather than only when the
	// settings form is saved: the forwarder reads the template from storage and
	// would otherwise keep publishing the old one indefinitely.
	cur.Template = migrateLegacyTemplate(cur.Template)
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
