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
	"github.com/haoyu010/ext.to/internal/scrape"
	"github.com/haoyu010/ext.to/internal/store"
	"github.com/haoyu010/ext.to/internal/telegram"
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
	f.log.Printf("forwarder: started, interval=%s", f.cfg.Get().Duration())
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
	f.log.Printf("forwarder: stopped")
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
			f.log.Printf("forwarder: flush state: %v", err)
		}
		f.log.Printf("forwarder: cycle done found=%d new=%d sent=%d skipped=%d failed=%d baseline=%v",
			rep.Found, rep.New, rep.Sent, rep.Skipped, rep.Failed, rep.Baseline)
	}()

	if err := f.run(ctx, &rep); err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		f.state.SetLastError(err.Error())
		f.log.Printf("forwarder: cycle error: %v", err)
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
		f.log.Printf("forwarder: baseline established with %d existing torrents; "+
			"new posts will be forwarded from the next cycle", len(items))
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

	for i, it := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			// Keep well under Telegram's per-chat rate limit.
			time.Sleep(1200 * time.Millisecond)
		}
		if err := f.publish(ctx, client, tg, settings, it, threadID); err != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, fmt.Sprintf("%d: %v", it.ID, err))
			f.state.MarkError(it.ID, err.Error())
			f.log.Printf("forwarder: failed to forward %d (%s): %v", it.ID, it.Title, err)
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

// publish resolves extras (magnet, poster) and sends one torrent.
func (f *Forwarder) publish(ctx context.Context, client *scrape.Client, tg *telegram.Client,
	settings config.Settings, it scrape.Item, threadID int) error {

	var magnet string
	if settings.WithMagnet {
		m, err := client.FetchMagnet(ctx, it)
		if err != nil {
			// A missing magnet should not block the post.
			f.log.Printf("forwarder: magnet unavailable for %d: %v", it.ID, err)
		} else {
			magnet = m
		}
	}

	caption := config.Render(settings.Template, config.TemplateData{
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
	})

	var photo []byte
	if settings.WithPoster {
		if p, err := client.FetchPoster(ctx, it); err != nil {
			f.log.Printf("forwarder: poster lookup failed for %d: %v", it.ID, err)
		} else if p != "" {
			b, err := client.DownloadImage(ctx, p)
			if err != nil {
				f.log.Printf("forwarder: poster download failed for %d: %v", it.ID, err)
			} else {
				photo = b
			}
		}
	}

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
	f.state.MarkSent(it.ID, magnet)
	return nil
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
		f.log.Printf("forwarder: flush after manual check: %v", ferr)
	}
	if err != nil {
		f.state.SetLastError(err.Error())
		return rep, err
	}
	f.state.SetLastError("")
	return rep, nil
}

// TestTelegram verifies the bot token and chat id.
func (f *Forwarder) TestTelegram(ctx context.Context, s config.Settings) (string, error) {
	if s.BotToken == "" {
		return "", errors.New("bot token is empty")
	}
	client := telegram.New(s.BotToken)
	me, err := client.GetMe(ctx)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Connected as @%s (id %d)", me.Username, me.ID)
	if s.ChatID == "" {
		return msg, nil
	}
	threadID, err := parseThreadID(s.MessageTopic)
	if err != nil {
		return msg, err
	}
	if _, err := client.Send(ctx, telegram.Post{
		ChatID:   s.ChatID,
		ThreadID: threadID,
		Caption:  "✅ <b>ext.to forwarder</b>\nThis chat is configured correctly.",
		Silent:   true,
	}, nil, ""); err != nil {
		return msg, fmt.Errorf("bot works but chat %q is unreachable: %w", s.ChatID, err)
	}
	return msg + fmt.Sprintf("; test message delivered to %s", s.ChatID), nil
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
