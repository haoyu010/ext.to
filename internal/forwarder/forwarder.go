// Package forwarder ties scraping, filtering, storage and Telegram delivery
// together and runs them on a schedule.
package forwarder

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/media"
	"github.com/haoyu010/ext.to/internal/scrape"
	"github.com/haoyu010/ext.to/internal/store"
	"github.com/haoyu010/ext.to/internal/telegram"
	"github.com/haoyu010/ext.to/internal/tmdb"
)

// Forwarder scans ext.to and publishes new torrents.
type Forwarder struct {
	cfg   *config.Store
	state *store.Store
	log   *log.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	// lastRun summarises the most recent scan for the web UI.
	lastRun   RunReport
	lastRunMu sync.RWMutex
}

// RunReport summarises a single scan cycle.
type RunReport struct {
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Found     int       `json:"found"`
	New       int       `json:"new"`
	Sent      int       `json:"sent"`
	Skipped   int       `json:"skipped"`
	Failed    int       `json:"failed"`
	Baseline  bool      `json:"baseline"`
	Errors    []string  `json:"errors,omitempty"`
}

// errNoMatch marks a torrent that was intentionally not forwarded because
// TMDBOnly is enabled and no TMDB entry could be found. It is a skip, not a
// delivery failure, so it must not inflate the failure count.
var errNoMatch = errors.New("no tmdb match")

// New creates a forwarder.
func New(cfg *config.Store, state *store.Store, logger *log.Logger) *Forwarder {
	if logger == nil {
		logger = log.Default()
	}
	return &Forwarder{cfg: cfg, state: state, log: logger}
}

// Start launches the polling loop. It is safe to call when already running.
func (f *Forwarder) Start() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan struct{})
	go f.loop(ctx, f.done)
	f.log.Printf("监听已启动，间隔 %s", f.cfg.Get().Duration())
}

// Stop halts the polling loop and waits for the current cycle to finish.
func (f *Forwarder) Stop() {
	f.mu.Lock()
	cancel, done := f.cancel, f.done
	f.cancel, f.done = nil, nil
	f.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	f.log.Printf("监听已停止")
}

// Running reports whether the polling loop is active.
func (f *Forwarder) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancel != nil
}

// LastRun returns the summary of the most recent cycle.
func (f *Forwarder) LastRun() RunReport {
	f.lastRunMu.RLock()
	defer f.lastRunMu.RUnlock()
	return f.lastRun
}

func (f *Forwarder) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	// Run once immediately so the operator sees results without waiting.
	f.cycle(ctx)

	for {
		interval := f.cfg.Get().Duration()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			f.cycle(ctx)
		}
	}
}

func (f *Forwarder) cycle(ctx context.Context) {
	rep := RunReport{StartedAt: time.Now()}
	defer func() {
		rep.EndedAt = time.Now()
		f.lastRunMu.Lock()
		f.lastRun = rep
		f.lastRunMu.Unlock()
		f.state.TouchLastRun()
		if err := f.state.Flush(); err != nil {
			f.log.Printf("保存记录失败：%v", err)
		}
		f.log.Printf("本轮扫描结束：抓取 %d，新增匹配 %d，已推送 %d，跳过 %d，失败 %d，基线 %v",
			rep.Found, rep.New, rep.Sent, rep.Skipped, rep.Failed, rep.Baseline)
	}()

	if err := f.run(ctx, &rep); err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		f.state.SetLastError(err.Error())
		f.log.Printf("本轮扫描出错：%v", err)
		return
	}
	f.state.SetLastError("")
}

func (f *Forwarder) run(ctx context.Context, rep *RunReport) error {
	settings := f.cfg.Get()

	client, err := scrape.New(settings)
	if err != nil {
		return err
	}
	items, err := client.FetchList(ctx, scrape.FetchOptions{
		Categories: settings.Categories,
		Age:        settings.Age,
		MaxPages:   settings.MaxPages,
		Delay:      400 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	rep.Found = len(items)
	if len(items) == 0 {
		return nil
	}

	// Newest first: ext.to lists by age already, but sort defensively so the
	// baseline and the batch size apply to the most recent items.
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID > items[j].ID })

	filter, err := newFilter(settings)
	if err != nil {
		return err
	}

	// First ever scan only records a baseline so the channel is not flooded
	// with the entire current listing.
	if !f.state.FirstRun() {
		for _, it := range items {
			f.state.Put(recordFor(it, false))
		}
		f.state.MarkFirstRun()
		rep.Baseline = true
		rep.New = 0
		rep.Skipped = len(items)
		f.log.Printf("已建立基线：记录现有 %d 条种子，从下一轮开始推送新发布", len(items))
		return nil
	}

	candidates := make([]scrape.Item, 0, len(items))
	for _, it := range items {
		if f.state.Has(it.ID) {
			continue
		}
		// Record everything we have seen, even when filtered out, so the
		// filter decision is not revisited on every cycle.
		f.state.Put(recordFor(it, false))
		if !filter.allow(it) {
			rep.Skipped++
			continue
		}
		candidates = append(candidates, it)
	}
	rep.New = len(candidates)
	if len(candidates) == 0 {
		return nil
	}

	if len(candidates) > settings.BatchSize {
		rep.Skipped += len(candidates) - settings.BatchSize
		candidates = candidates[:settings.BatchSize]
	}

	tg := telegram.New(settings.BotToken)
	threadID, err := parseThreadID(settings.MessageTopic)
	if err != nil {
		return err
	}
	movies := tmdb.New(settings.TMDBKey, settings.TMDBLang)

	for i, it := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			// Keep well under Telegram's per-chat rate limit.
			time.Sleep(1200 * time.Millisecond)
		}
		if err := f.publish(ctx, client, tg, movies, settings, it, threadID); err != nil {
			if errors.Is(err, errNoMatch) {
				// Filtered out on purpose; neither sent nor a failure.
				rep.Skipped++
				f.log.Printf("跳过 %d（%s）：没有匹配到 TMDB 条目", it.ID, it.Title)
				continue
			}
			rep.Failed++
			rep.Errors = append(rep.Errors, fmt.Sprintf("%d: %v", it.ID, err))
			f.state.MarkError(it.ID, err.Error())
			f.log.Printf("推送失败 %d（%s）：%v", it.ID, it.Title, err)
			if telegram.IsBlocked(err) {
				// Nothing will succeed; stop the cycle early.
				return fmt.Errorf("telegram rejected the chat: %w", err)
			}
			continue
		}
		rep.Sent++
	}
	return nil
}

// publish resolves extras (TMDB match, magnet, poster) and sends one torrent.
func (f *Forwarder) publish(ctx context.Context, client *scrape.Client, tg *telegram.Client,
	movies *tmdb.Client, settings config.Settings, it scrape.Item, threadID int) error {

	// The detail page carries the poster, the IMDb id and the per-page magnet
	// token, so one request serves all three.
	needDetail := settings.WithMagnet || settings.WithPoster || movies.Configured()
	var detail scrape.Detail
	var page []byte
	if needDetail {
		var err error
		page, err = client.FetchDetailPage(ctx, it)
		if err != nil {
			// Losing the detail page degrades the post rather than failing it.
			f.log.Printf("%d 的详情页不可用：%v", it.ID, err)
			page = nil
		} else {
			detail = scrape.ParseDetail(page)
		}
	}

	entry, matched := f.matchTMDB(ctx, movies, settings, it, detail)
	if !matched && settings.TMDBOnly {
		// The release has no TMDB entry, which is a deliberate skip rather
		// than a delivery failure, so the run report stays meaningful.
		return errNoMatch
	}

	var magnet string
	if settings.WithMagnet && page != nil {
		m, err := client.MagnetFromDetail(ctx, it, page)
		if err != nil {
			// A missing magnet should not block the post.
			f.log.Printf("%d 的磁力链接不可用：%v", it.ID, err)
		} else {
			magnet = m
		}
	}

	caption := config.Render(settings.Template, templateData(it, entry, magnet))

	photo := f.loadPoster(ctx, client, settings, it, detail, entry)

	_, err := tg.Send(ctx, telegram.Post{
		ChatID:            settings.ChatID,
		ThreadID:          threadID,
		Caption:           caption,
		Silent:            settings.Silent,
		DisableWebPreview: settings.DisableWeb,
	}, photo, posterName(it))
	if err != nil {
		return err
	}
	f.state.MarkSentWithTMDB(it.ID, magnet, store.TMDBInfo{
		ID:     entry.ID,
		Type:   entry.Type,
		Title:  entry.Title,
		Year:   entry.Year,
		Rating: entry.Rating,
	})
	return nil
}

// matchTMDB resolves the release against TMDB. A nil or unconfigured client,
// or any lookup failure, simply yields no match: TMDB is an enrichment, and
// the forwarder must keep working when the API is unreachable.
func (f *Forwarder) matchTMDB(ctx context.Context, movies *tmdb.Client, settings config.Settings,
	it scrape.Item, detail scrape.Detail) (tmdb.Entry, bool) {

	if !movies.Configured() {
		return tmdb.Entry{}, false
	}
	// The release name supplies the year, season and kind; the detail page's
	// canonical title is offered as the preferred search term. Resolve tries
	// both, since a series page reports the original name rather than the
	// English one.
	entry, err := movies.Resolve(ctx, detail.IMDbID, it.Title, detail.Title)
	if err != nil {
		if !errors.Is(err, tmdb.ErrNoMatch) && !errors.Is(err, tmdb.ErrNotConfigured) {
			f.log.Printf("%d 的 TMDB 查询失败：%v", it.ID, err)
		}
		return tmdb.Entry{}, false
	}
	f.log.Printf("%d 匹配到 TMDB：%s（%s %d），匹配方式 %s",
		it.ID, entry.Title, entry.Type, entry.Year, entry.MatchedBy)
	return entry, true
}

// loadPoster returns the poster bytes to upload, honouring PosterSource.
func (f *Forwarder) loadPoster(ctx context.Context, client *scrape.Client, settings config.Settings,
	it scrape.Item, detail scrape.Detail, entry tmdb.Entry) []byte {

	if !settings.WithPoster {
		return nil
	}
	fromTMDB := settings.PosterSource != config.PosterSourceTracker
	fromTracker := settings.PosterSource != config.PosterSourceTMDB

	if fromTMDB && entry.ID != 0 {
		if url := entry.PosterURL(500); url != "" {
			if b, err := client.DownloadImage(ctx, url); err == nil {
				return b
			} else {
				f.log.Printf("%d 的 TMDB 海报下载失败：%v", it.ID, err)
			}
		}
	}
	if !fromTracker {
		return nil
	}
	if detail.PosterURL == "" {
		return nil
	}
	if b, err := client.DownloadImage(ctx, detail.PosterURL); err != nil {
		f.log.Printf("%d 的海报下载失败：%v", it.ID, err)
		return nil
	} else {
		return b
	}
}

// templateData builds the caption value set for one torrent.
func templateData(it scrape.Item, entry tmdb.Entry, magnet string) config.TemplateData {
	d := config.TemplateData{
		Title:    it.Title,
		Category: it.Category,
		Size:     it.Size,
		Files:    it.Files,
		Seeds:    it.Seeds,
		Leeches:  it.Leeches,
		Age:      it.Age,
		Source:   it.Uploader,
		Uploader: it.Uploader,
		URL:      it.URL,
		Magnet:   magnet,
		ID:       it.ID,
	}
	if entry.ID != 0 {
		d.TMDBTitle = entry.Title
		d.TMDBOriginalTitle = entry.OriginalTitle
		d.TMDBYear = entry.Year
		d.TMDBRating = entry.Rating
		d.TMDBVotes = entry.VoteCount
		d.TMDBURL = entry.URL()
		d.TMDBID = entry.ID
		d.TMDBType = entry.Type
		d.TMDBOverview = entry.Overview
	}
	return d
}

func recordFor(it scrape.Item, sent bool) store.Record {
	return store.Record{
		ID:       it.ID,
		Slug:     it.Slug,
		Title:    it.Title,
		Category: it.Category,
		Size:     it.Size,
		SizeMB:   it.SizeMB,
		URL:      it.URL,
		Sent:     sent,
	}
}

// TestResult reports a dry run outcome without publishing anything.
type TestResult struct {
	// Fetched is the number of torrents read from the listing.
	Fetched int `json:"fetched"`
	// Matched is how many of those pass the current filters.
	Matched int `json:"matched"`
	// Unseen is how many matched torrents have not been recorded yet, so the
	// operator can tell whether a scan would actually post anything.
	Unseen   int           `json:"unseen"`
	Baseline bool          `json:"baseline"`
	Items    []scrape.Item `json:"items"`
	Preview  string        `json:"preview"`
}

// Test fetches the current listing and reports what would be forwarded,
// without touching the state store or Telegram.
func (f *Forwarder) Test(ctx context.Context, sample int) (*TestResult, error) {
	settings := f.cfg.Get()
	client, err := scrape.New(settings)
	if err != nil {
		return nil, err
	}
	items, err := client.FetchList(ctx, scrape.FetchOptions{
		Categories: settings.Categories,
		Age:        settings.Age,
		MaxPages:   settings.MaxPages,
		Delay:      300 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID > items[j].ID })

	filter, err := newFilter(settings)
	if err != nil {
		return nil, err
	}
	res := &TestResult{Fetched: len(items), Baseline: !f.state.FirstRun()}
	for _, it := range items {
		if !filter.allow(it) {
			continue
		}
		res.Matched++
		if !f.state.Has(it.ID) {
			res.Unseen++
		}
		if sample <= 0 || len(res.Items) < sample {
			res.Items = append(res.Items, it)
		}
	}
	if len(res.Items) > 0 {
		first := res.Items[0]
		res.Preview = config.Render(settings.Template, config.TemplateData{
			Title:    first.Title,
			Category: first.Category,
			Size:     first.Size,
			Files:    first.Files,
			Seeds:    first.Seeds,
			Leeches:  first.Leeches,
			Age:      first.Age,
			Source:   first.Uploader,
			Uploader: first.Uploader,
			URL:      first.URL,
			Magnet:   "magnet:?xt=urn:btih:EXAMPLE",
			ID:       first.ID,
		})
	}
	return res, nil
}

// ForceCheck runs a scan immediately, outside the timer.
func (f *Forwarder) ForceCheck(ctx context.Context) (RunReport, error) {
	rep := RunReport{StartedAt: time.Now()}
	err := f.run(ctx, &rep)
	rep.EndedAt = time.Now()
	f.lastRunMu.Lock()
	f.lastRun = rep
	f.lastRunMu.Unlock()
	if ferr := f.state.Flush(); ferr != nil {
		f.log.Printf("手动检查后保存记录失败：%v", ferr)
	}
	if err != nil {
		f.state.SetLastError(err.Error())
		return rep, err
	}
	f.state.SetLastError("")
	return rep, nil
}

// TestTMDB verifies that the API key works and reports the account's
// configured language, so the operator gets confirmation without waiting for
// a scan.
func (f *Forwarder) TestTMDB(ctx context.Context, key, lang string) (string, error) {
	client := tmdb.New(key, lang)
	if !client.Configured() {
		return "", tmdb.ErrNotConfigured
	}
	// A well-known title exercises both the key and the language setting.
	entry, err := client.Resolve(ctx, "tt1375666", "Inception.2010.1080p.BluRay", "")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Resolved Inception to %q (%s %d) with language %s",
		entry.Title, entry.Type, entry.Year, langOrDefault(lang)), nil
}

// LookupTMDB resolves one title on demand, returning the parsed release name
// alongside the match so the dashboard can show how the name was read.
//
// key and lang come from the settings form rather than from storage, because
// the operator usually tests a key before deciding to save it. An empty value
// falls back to the stored setting, so the lookup still works after the form
// has been cleared.
func (f *Forwarder) LookupTMDB(ctx context.Context, imdbID, title, key, lang string) (media.Result, tmdb.Entry, error) {
	settings := f.cfg.Get()
	if strings.TrimSpace(key) == "" {
		key = settings.TMDBKey
	}
	if strings.TrimSpace(lang) == "" {
		lang = settings.TMDBLang
	}
	client := tmdb.New(key, lang)
	if !client.Configured() {
		return media.Parse(title), tmdb.Entry{}, tmdb.ErrNotConfigured
	}
	entry, err := client.Resolve(ctx, imdbID, title, "")
	if err != nil {
		return media.Parse(title), tmdb.Entry{}, err
	}
	return media.Parse(title), entry, nil
}

func langOrDefault(lang string) string {
	if strings.TrimSpace(lang) == "" {
		return "zh-CN"
	}
	return lang
}

// TestTelegram verifies the bot token, resolves the destination, and sends a
// real message to it.
//
// The permission checks are separate from the send so a misconfiguration is
// reported as a specific problem. A bare send failure only says "Forbidden",
// which is the same response for a wrong id, a bot that was never added, and a
// channel where the bot lacks the right to post -- three different fixes.
func (f *Forwarder) TestTelegram(ctx context.Context, s config.Settings) (string, error) {
	if s.BotToken == "" {
		return "", errors.New("机器人 Token 为空")
	}
	client := telegram.New(s.BotToken)
	me, err := client.GetMe(ctx)
	if err != nil {
		return "", fmt.Errorf("Token 无效：%w", err)
	}
	msg := fmt.Sprintf("已连接机器人 @%s（ID %d）", me.Username, me.ID)
	if s.ChatID == "" {
		return msg, errors.New("尚未填写目标频道，请填写 @频道用户名 或 -100 开头的频道 ID")
	}

	ref := normalizeChatRef(s.ChatID)
	chat, err := client.GetChat(ctx, ref)
	if err != nil {
		return msg, fmt.Errorf("无法访问频道 %s：%w\n\n"+
			"请确认：1) 已把机器人加入该频道；2) 已将其设为管理员。"+
			"若使用 @用户名 形式，频道必须是公开的", s.ChatID, err)
	}
	where := chat.Display()
	if chat.IsChannel() {
		msg += fmt.Sprintf("；目标为频道 %s", where)
	} else {
		msg += fmt.Sprintf("；目标为群组 %s（不是频道，需在频道管理员中填写）", where)
	}

	member, err := client.GetChatMember(ctx, ref, me.ID)
	if err != nil {
		return msg, fmt.Errorf("无法读取机器人在 %s 中的权限：%w", where, err)
	}
	if !member.IsAdmin() {
		return msg, fmt.Errorf("机器人不是 %s 的管理员（当前身份 %s）。"+
			"请在频道设置 → 管理员中添加该机器人，并勾选「发布消息」", where, member.Status)
	}
	if !member.CanPost(chat.IsChannel()) {
		return msg, fmt.Errorf("机器人已是 %s 的管理员，但没有「发布消息」权限，请补充勾选", where)
	}

	threadID, err := parseThreadID(s.MessageTopic)
	if err != nil {
		return msg, err
	}
	if threadID > 0 && chat.IsChannel() {
		return msg, fmt.Errorf("频道不支持论坛话题，请清空「论坛话题」")
	}
	if threadID > 0 && !chat.IsForum {
		return msg, fmt.Errorf("%s 未开启话题功能，无法使用「论坛话题」，请清空后再试", where)
	}

	if _, err := client.Send(ctx, telegram.Post{
		ChatID:   s.ChatID,
		ThreadID: threadID,
		Caption:  "✅ <b>ext.to 转发面板</b>\n频道配置正确，稍后会向这里推送新种子。",
		Silent:   true,
	}, nil, ""); err != nil {
		return msg, fmt.Errorf("频道 %s 校验通过但发送失败：%w", where, err)
	}
	return msg + "；测试消息已发送", nil
}

// filter applies include/exclude patterns and size bounds.
type filter struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
	minMB   float64
	maxMB   float64
}

func newFilter(s config.Settings) (*filter, error) {
	f := &filter{minMB: s.MinSizeMB, maxMB: s.MaxSizeMB}
	for _, p := range s.Include {
		re, err := compilePattern(p)
		if err != nil {
			return nil, err
		}
		if re != nil {
			f.include = append(f.include, re)
		}
	}
	for _, p := range s.Exclude {
		re, err := compilePattern(p)
		if err != nil {
			return nil, err
		}
		if re != nil {
			f.exclude = append(f.exclude, re)
		}
	}
	return f, nil
}

func compilePattern(p string) (*regexp.Regexp, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil, nil
	}
	re, err := regexp.Compile("(?i)" + p)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
	}
	return re, nil
}

func (f *filter) allow(it scrape.Item) bool {
	if len(f.include) > 0 {
		hit := false
		for _, re := range f.include {
			if re.MatchString(it.Title) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	for _, re := range f.exclude {
		if re.MatchString(it.Title) {
			return false
		}
	}
	// Size filters only apply when the size is known.
	if it.SizeMB > 0 {
		if f.minMB > 0 && it.SizeMB < f.minMB {
			return false
		}
		if f.maxMB > 0 && it.SizeMB > f.maxMB {
			return false
		}
	}
	return true
}

// parseThreadID accepts a numeric topic id or a Telegram topic link such as
// https://t.me/c/123456789/42 (the trailing number is the topic).
func parseThreadID(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if i := strings.LastIndex(raw, "/"); i >= 0 {
		raw = raw[i+1:]
	}
	raw = strings.TrimPrefix(raw, "topic")
	id, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || id < 0 {
		return 0, fmt.Errorf("invalid topic id %q: expected a number or a t.me topic link", raw)
	}
	return id, nil
}

func posterName(it scrape.Item) string {
	return fmt.Sprintf("%d.jpg", it.ID)
}

// LookupChat resolves a channel reference and reports whether the bot may post
// there. It exists because the channel id is the hardest field to fill in:
// Telegram only reveals -100... ids through the API, and copying one from a
// message link is error-prone. An operator can paste an @username, a t.me link
// or an id and see what it resolves to before saving it.
//
// token comes from the settings form so the id can be checked before either
// field is saved, matching how the Telegram test already works; an empty value
// falls back to the stored token.
func (f *Forwarder) LookupChat(ctx context.Context, ref, token string) (telegram.Chat, telegram.ChatMember, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return telegram.Chat{}, telegram.ChatMember{}, errors.New("请填写频道用户名、链接或 ID")
	}
	if strings.TrimSpace(token) == "" {
		token = f.cfg.Get().BotToken
	}
	if strings.TrimSpace(token) == "" {
		return telegram.Chat{}, telegram.ChatMember{}, errors.New("请先填写并保存机器人 Token")
	}
	client := telegram.New(token)
	chat, err := client.GetChat(ctx, normalizeChatRef(ref))
	if err != nil {
		return telegram.Chat{}, telegram.ChatMember{}, err
	}
	me, err := client.GetMe(ctx)
	if err != nil {
		return telegram.Chat{}, telegram.ChatMember{}, err
	}
	member, err := client.GetChatMember(ctx, normalizeChatRef(ref), me.ID)
	if err != nil {
		return *chat, telegram.ChatMember{}, err
	}
	return *chat, *member, nil
}

// normalizeChatRef accepts the forms an operator is likely to paste:
// "https://t.me/name", "t.me/name" and "@name" all mean "@name". A private
// channel's "https://t.me/c/123456789/45" link carries the id without the
// -100 prefix that the API expects.
func normalizeChatRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ref
	}
	lower := strings.ToLower(ref)
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/"} {
		if strings.HasPrefix(lower, prefix) {
			ref = ref[len(prefix):]
			lower = lower[len(prefix):]
			break
		}
	}
	// A private-channel link looks like c/123456789/45; the API wants the
	// internal id, which is -100 followed by the link's id.
	if strings.HasPrefix(lower, "c/") {
		parts := strings.Split(strings.Trim(ref[2:], "/"), "/")
		if len(parts) > 0 && parts[0] != "" {
			if _, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
				return "-100" + parts[0]
			}
		}
	}
	// Trim a trailing topic segment from a public link: t.me/name/42.
	if i := strings.Index(ref, "/"); i >= 0 {
		ref = ref[:i]
	}
	if ref != "" && !strings.HasPrefix(ref, "@") && !strings.HasPrefix(ref, "-") {
		if _, err := strconv.ParseInt(ref, 10, 64); err != nil {
			return "@" + ref
		}
	}
	return ref
}
