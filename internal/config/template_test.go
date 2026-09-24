package config

import (
	"regexp"
	"strings"
	"testing"
)

// A matched release must render the TMDB values.
func TestRenderTMDBFields(t *testing.T) {
	got := Render("{tmdb_title}|{tmdb_year}|{tmdb_rating}|{tmdb_votes}|{tmdb_url}|{tmdb_id}|{tmdb_type}",
		TemplateData{
			Title:      "Glass.Onion.2022.720p",
			TMDBTitle:  "利刃出鞘2",
			TMDBYear:   2022,
			TMDBRating: 7.14,
			TMDBVotes:  533052,
			TMDBURL:    "https://www.themoviedb.org/movie/661374",
			TMDBID:     661374,
			TMDBType:   "movie",
		})
	want := "利刃出鞘2|2022|7.1|533k|https://www.themoviedb.org/movie/661374|661374|movie"
	if got != want {
		t.Errorf("Render =\n  %q\nwant\n  %q", got, want)
	}
}

// A matched series must render the season the release stated, and it must come
// from the release name rather than from TMDB: a TMDB series covers every
// season in one entry, so the entry can only say which work the release is.
//
// The shipped preset is rendered rather than a copy of it, because the cases
// below are exactly the ones a hand-written copy would not exercise: the label
// carries its own leading space and the year its own brackets, so a template
// with either written literally renders "名称 第 0 季" or "()".
func TestRenderSeasonLabel(t *testing.T) {
	cases := []struct {
		name string
		data TemplateData
		head string
	}{
		{
			name: "series with a stated season",
			data: TemplateData{TMDBTitle: "一人之下", TMDBYear: 2016, TMDBID: 67063, TMDBType: "tv", Season: 6},
			head: "片名：一人之下 第 6 季",
		},
		{
			name: "film states no season",
			data: TemplateData{TMDBTitle: "流浪地球2", TMDBYear: 2023, TMDBID: 842675, TMDBType: "movie"},
			head: "片名：流浪地球2",
		},
		{
			name: "series with no stated season",
			data: TemplateData{TMDBTitle: "三体", TMDBYear: 2023, TMDBID: 204541, TMDBType: "tv"},
			head: "片名：三体",
		},
		{
			// TMDB files some seasons as their own entry named after them, so
			// the title already says which season it is.
			name: "title already names the season",
			data: TemplateData{TMDBTitle: "毛骗 第二季 (2011)", TMDBID: 259602, TMDBType: "tv", Season: 2},
			head: "片名：毛骗 第二季 (2011)",
		},
		{
			// Some entries carry no air date at all; the year line must vanish
			// rather than be published as a bare label.
			name: "entry has no year",
			data: TemplateData{TMDBTitle: "某剧", TMDBID: 1, TMDBType: "tv", Season: 3},
			head: "片名：某剧 第 3 季",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Render(TMDBTemplate, tc.data)
			first := strings.SplitN(got, "\n", 2)[0]
			if first != tc.head {
				t.Errorf("first line =\n  %q\nwant\n  %q", first, tc.head)
			}
		})
	}
	// A season of zero is not a season: an unmatched release, or a complete
	// pack, must not render "第 0 季".
	if got := Render("{season}|{season_label}|{episode}", TemplateData{}); got != "||" {
		t.Errorf("empty numbers rendered as %q, want three empty fields", got)
	}
}

// A labelled preset is written once and rendered for every release, but not
// every field exists for every one. A line whose value is missing must be
// dropped rather than published as a bare label: "年份：" with nothing after it
// promises a value and delivers none.
func TestRenderDropsEmptyLabelLines(t *testing.T) {
	// No year, no reference and no overview: all three lines go.
	got := Render(TMDBTemplate, TemplateData{
		Title: "毛骗 第二季", TMDBTitle: "毛骗 第二季 (2011)",
		TMDBID: 259602, TMDBType: "tv", Season: 2,
		Size: "1.43 GB", Uploader: "UnclePanda",
		Magnet: "magnet:?xt=urn:btih:DEADBEEF",
	})
	for _, dangling := range []string{"年份：\n", "TMDB：\n", "简介：\n", "片名：\n"} {
		if strings.Contains(got, dangling+"\n") || strings.HasSuffix(got, dangling) {
			t.Errorf("caption kept a bare label %q:\n%s", dangling, got)
		}
	}
	// The lines that do have values must still be there.
	for _, want := range []string{"片名：毛骗 第二季 (2011)", "大小：1.43 GB", "分享：UnclePanda"} {
		if !strings.Contains(got, want) {
			t.Errorf("caption lost %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "\n\n\n"); n != 0 {
		t.Errorf("dropping lines left a blank gap:\n%q", got)
	}

	// A value that is deliberately present but short must be kept, so the rule
	// cannot be "drop the line when the value looks empty".
	if got := Render("大小：{size}", TemplateData{Size: "NG"}); got != "大小：NG" {
		t.Errorf("Render = %q, want the stated value kept", got)
	}
	// Prose ending in a colon is not a label and must survive.
	prose := "本片改编自同名小说，讲述了一桩跨越二十年的旧案："
	if got := Render("简介：{tmdb_overview}", TemplateData{TMDBOverview: prose}); got != "简介："+prose {
		t.Errorf("Render = %q, want the prose kept", got)
	}
}

// The torrent link must always resolve to something usable. A magnet is what a
// reader wants; when the per-page magnet could not be resolved the detail page
// is the honest fallback, and the label follows the destination so the caption
// never promises a magnet it does not have.
func TestRenderTorrentLinkFallsBackToDetailPage(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:DEADBEEF&tr=udp%3A%2F%2Ft.example%3A6969"
	got := Render("<a href=\"{torrent_url}\">{torrent_label}</a>", TemplateData{
		Magnet: magnet, URL: "https://ext.to/y-11/",
	})
	if !strings.Contains(got, "种子链接") {
		t.Errorf("with a magnet the label should say 种子链接: %q", got)
	}
	// The query separator has to be escaped or Telegram reads the link as
	// truncated at the first ampersand.
	if !strings.Contains(got, "DEADBEEF&amp;tr=") {
		t.Errorf("magnet was not escaped for HTML: %q", got)
	}

	got = Render("<a href=\"{torrent_url}\">{torrent_label}</a>", TemplateData{
		URL: "https://ext.to/y-11/",
	})
	if !strings.Contains(got, "详情页") || !strings.Contains(got, "https://ext.to/y-11/") {
		t.Errorf("without a magnet the caption should fall back to the detail page: %q", got)
	}
	if strings.Contains(got, "magnet") {
		t.Errorf("no magnet was available, so none may be advertised: %q", got)
	}

	// Neither a magnet nor a detail page: the whole line has to go. Publishing
	// "<a href=\"\">详情页</a>" would be a dead link in a sent message.
	if got := Render("正文\n<a href=\"{torrent_url}\">{torrent_label}</a>", TemplateData{}); got != "正文" {
		t.Errorf("a link with no destination was published: %q", got)
	}
	// A line that pairs the link with other text is content, not a bare link,
	// and must be left for the operator to decide about.
	kept := Render("正文\n<a href=\"{torrent_url}\">{torrent_label}</a> · {size}", TemplateData{Size: "1 GB"})
	if !strings.Contains(kept, "· 1 GB") || !strings.Contains(kept, `href=""`) {
		t.Errorf("a link line carrying other text was not left alone: %q", kept)
	}
}

// The TMDB reference is printed as text, not as a link, so a caption shows
// which entry matched without sending the reader to another site. It has to be
// one token built by the renderer: a template writing "{tmdb_type}/{tmdb_id}"
// renders "/0" for a release that matched nothing.
func TestRenderTMDBRefIsTextAndEmptyWhenUnmatched(t *testing.T) {
	if got := Render("{tmdb_ref}", TemplateData{TMDBID: 287994, TMDBType: "tv"}); got != "tv/287994" {
		t.Errorf("tmdb_ref = %q, want tv/287994", got)
	}
	if got := Render("{tmdb_ref}", TemplateData{}); got != "" {
		t.Errorf("tmdb_ref for an unmatched release = %q, want empty", got)
	}
	// The shipped preset must not link the entry.
	if strings.Contains(TMDBTemplate, "tmdb_url") {
		t.Error("TMDBTemplate should print the reference as text, not link it")
	}
}

// An unmatched release must not render dangling values or stray separators.
func TestRenderUnmatchedFallsBackToTorrentFields(t *testing.T) {
	got := Render("{tmdb_title}|{tmdb_year}|{tmdb_rating}|{tmdb_votes}|{tmdb_url}",
		TemplateData{
			Title: "Lian Ross - V (Album) (2026)",
			URL:   "https://ext.to/lian-ross-22395846/",
		})
	want := "Lian Ross - V (Album) (2026)||||https://ext.to/lian-ross-22395846/"
	if got != want {
		t.Errorf("Render =\n  %q\nwant\n  %q", got, want)
	}
}

// A title containing markup must be escaped so Telegram accepts the caption.
func TestRenderEscapesTMDBTitle(t *testing.T) {
	got := Render("{tmdb_title}", TemplateData{TMDBTitle: "Tom & Jerry <2021>"})
	if !strings.Contains(got, "Tom &amp; Jerry &lt;2021&gt;") {
		t.Errorf("tmdb title was not escaped: %q", got)
	}
}

// The overview is truncated by rune count so multi-byte text stays intact.
func TestRenderTruncatesOverviewByRunes(t *testing.T) {
	long := strings.Repeat("字", 400)
	got := Render("{tmdb_overview}", TemplateData{TMDBOverview: long})
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected an ellipsis on a truncated overview, got %q", got[len(got)-10:])
	}
	if n := len([]rune(got)); n > 321 {
		t.Errorf("overview truncated to %d runes, want at most 321", n)
	}
	// Every rune must still be a valid CJK character plus the ellipsis.
	for _, r := range got {
		if r != '字' && r != '…' {
			t.Fatalf("multi-byte text was corrupted at %q", r)
		}
	}
}

func TestRenderDefaultTemplateWhenEmpty(t *testing.T) {
	got := Render("   ", TemplateData{Title: "Some Title"})
	if !strings.Contains(got, "Some Title") {
		t.Errorf("empty template should fall back to the default: %q", got)
	}
}

// humanCount is used for vote counts, where a compact form reads better.
func TestHumanCount(t *testing.T) {
	cases := map[int]string{0: "", -5: "", 42: "42", 999: "999", 1000: "1k", 533052: "533k", 2500000: "2.5m"}
	for in, want := range cases {
		if got := humanCount(in); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", in, got, want)
		}
	}
}

// An install that never edited its caption still carries a preset from an
// earlier release. Those presets link the tracker's detail page and never
// mention {magnet}, so such an install has no way to learn a torrent link
// exists; loading it has to move it onto the current preset.
//
// The match is byte for byte, so a template the operator edited is left alone:
// silently rewriting somebody's caption is worse than leaving an old one.
func TestLegacyTemplateMigratesOnlyWhenUntouched(t *testing.T) {
	old := "<b>{title}</b>\n\n📁 {category}\n💾 {size} · 📄 {files} files\n" +
		"🌱 {seeds} seeders · {leeches} leechers\n🕐 {age}\n\n" +
		`<a href="{url}">Open on ext.to</a>`
	if got := migrateLegacyTemplate(old); got != DefaultTemplate {
		t.Errorf("the old default preset was not migrated:\n%q", got)
	}
	oldTMDB := "<b>{tmdb_title}</b>{season_label}{tmdb_year_paren}\n" +
		"⭐ {tmdb_rating}/10 · {tmdb_votes} votes\n\n📁 {category_tmdb}\n" +
		"💾 {size} · 📄 {files} files\n🌱 {seeds} seeders · {leeches} leechers\n" +
		"🕐 {age}\n\n" + `<a href="{tmdb_url}">TMDB</a> · <a href="{url}">ext.to</a>`
	if got := migrateLegacyTemplate(oldTMDB); got != TMDBTemplate {
		t.Errorf("the old TMDB preset was not migrated:\n%q", got)
	}

	// An edited template stays as written, even one that began as a preset.
	mine := old + "\n我自己加的一行"
	if got := migrateLegacyTemplate(mine); got != mine {
		t.Errorf("an edited template was rewritten:\n%q", got)
	}
	if got := migrateLegacyTemplate(""); got != "" {
		t.Errorf("an empty template was changed to %q", got)
	}
	// The current presets are not legacy and must survive a second load.
	if got := migrateLegacyTemplate(TMDBTemplate); got != TMDBTemplate {
		t.Errorf("the current preset is not stable across loads:\n%q", got)
	}
}

// The shipped presets must link the torrent rather than the tracker page, and
// must not leave a dead link when no magnet could be resolved.
func TestShippedTemplatesLinkTheTorrent(t *testing.T) {
	for name, tpl := range map[string]string{"DefaultTemplate": DefaultTemplate, "TMDBTemplate": TMDBTemplate} {
		if !strings.Contains(tpl, "{torrent_url}") {
			t.Errorf("%s does not link the torrent", name)
		}
		if strings.Contains(tpl, `href="{url}"`) {
			t.Errorf("%s links the detail page where the torrent is expected", name)
		}
		out := Render(tpl, TemplateData{
			Title: "某片 2026", Size: "1.43 GB", Magnet: "magnet:?xt=urn:btih:DEADBEEF",
			TMDBTitle: "某片", TMDBYear: 2026, TMDBID: 287994, TMDBType: "tv",
		})
		if !strings.Contains(out, "magnet:?xt=urn:btih:DEADBEEF") {
			t.Errorf("%s did not render the magnet:\n%s", name, out)
		}
	}
}

// Telegram rejects a photo caption longer than 1024 characters, and the TMDB
// preset carries the overview, which is the largest value in it. The budget is
// measured rather than assumed, because the preset is written once and has to
// survive the longest values the fields can hold.
//
// The limit is counted over the visible text: a link's href is markup, not
// text. That distinction decides whether the preset is usable at all, because
// a real magnet carries every tracker and runs to roughly 1140 characters on
// its own (measured on the deployed install) — five times the caption limit if
// the href were counted.
func TestShippedTemplateFitsTheCaptionLimit(t *testing.T) {
	worst := TemplateData{
		Title:        strings.Repeat("超长发布名 ", 20),
		Category:     "Movies - Highres Movies",
		RuleCategory: "国产剧",
		Size:         "15.12GB", Files: 12, Seeds: 999, Leeches: 999,
		Age: "5 minutes ago", Uploader: strings.Repeat("发布者", 10),
		TMDBTitle: strings.Repeat("片名", 30), TMDBYear: 2026,
		TMDBID: 287994, TMDBType: "tv", Season: 12, Episode: 120,
		TMDBOverview: strings.Repeat("简介正文", 300),
		Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) +
			strings.Repeat("&tr=udp://tracker.example:6969/announce", 26),
	}
	for name, tpl := range map[string]string{"DefaultTemplate": DefaultTemplate, "TMDBTemplate": TMDBTemplate} {
		out := Render(tpl, worst)
		visible := len([]rune(reTag.ReplaceAllString(out, "")))
		if visible > 1024 {
			t.Errorf("%s renders %d visible runes, over Telegram's 1024 caption limit:\n%s",
				name, visible, out)
		}
	}
}

// reTag strips Telegram's HTML tags, leaving the text a reader sees. It is the
// quantity the caption limit is counted over.
var reTag = regexp.MustCompile(`<[^>]*>`)

// The shipped templates must only use placeholders the renderer knows.
//
// The keys are read out of the templates rather than listed here, because a
// list is a second place to forget: it was written before a placeholder was
// added to the preset and stayed green, and the panel offers whatever
// TemplateFields holds, so an unlisted key would be an undocumented one.
func TestTMDBTemplatePlaceholdersAreAllSupported(t *testing.T) {
	known := map[string]bool{}
	for _, f := range TemplateFields {
		known[f.Key] = true
	}
	for name, tpl := range map[string]string{"DefaultTemplate": DefaultTemplate, "TMDBTemplate": TMDBTemplate} {
		found := 0
		for _, m := range rePlaceholder.FindAllString(tpl, -1) {
			found++
			if !known[m] {
				t.Errorf("%s uses %s but TemplateFields does not list it", name, m)
			}
		}
		if found == 0 {
			t.Fatalf("%s yields no placeholders; the extraction is broken", name)
		}
	}
	// Rendering it with a match must leave no placeholder behind.
	out := Render(TMDBTemplate, TemplateData{
		Title: "t", URL: "u", TMDBTitle: "匹配", TMDBYear: 2022,
		TMDBRating: 7.1, TMDBVotes: 1000, TMDBURL: "tu", TMDBID: 5, TMDBType: "movie",
	})
	// Only the keys the renderer knows are looked for, because text the values
	// themselves carry may contain braces.
	for _, f := range TemplateFields {
		if strings.Contains(out, f.Key) {
			t.Errorf("TMDBTemplate left %s unrendered", f.Key)
		}
	}
}

// rePlaceholder matches a template placeholder. It is deliberately the same
// shape the renderer substitutes: a brace pair with no braces inside.
var rePlaceholder = regexp.MustCompile(`\{[a-z_]+\}`)
