package config

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
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
// is the honest fallback, and the text shown follows the destination so the
// caption never promises a magnet it does not have.
//
// The text is the link itself, trackers and all: a magnet without a tracker is
// the info hash alone, which a torrent client can still open but a cloud
// download service cannot -- it shows the hash where the file names should be.
//
// A magnet is printed inside <code> and a detail page is printed bare, because
// Telegram treats the two differently: it accepts no magnet: URL as a hyperlink
// at all, while a bare https URL is linkified by Telegram itself. The two were
// measured against the live Bot API, and so was the trap in between: an anchor
// wrapping a <code> span loses its href even for an https URL, which is why the
// detail page is never wrapped.
func TestRenderTorrentLinkFallsBackToDetailPage(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:DEADBEEF&tr=udp%3A%2F%2Ft.example%3A6969"
	got := Render("{torrent_text}", TemplateData{
		Magnet: magnet, URL: "https://ext.to/y-11/",
	})
	if want := `<code>` + escape(magnet) + `</code>`; got != want {
		t.Errorf("Render =\n  %q\nwant\n  %q", got, want)
	}
	// The magnet is a legal URI: what the site hands over is not, because the
	// release name in dn carries raw spaces.
	if strings.Contains(got, " ") {
		t.Errorf("the printed magnet still holds a raw space: %q", got)
	}

	got = Render("{torrent_text}", TemplateData{
		URL: "https://ext.to/y-11/",
	})
	// Bare, so Telegram linkifies it: an anchor around a <code> span would lose
	// the href, and the fallback link has to keep working.
	if want := "https://ext.to/y-11/"; got != want {
		t.Errorf("without a magnet the caption should show the detail page: %q", got)
	}
	if strings.Contains(got, "magnet") {
		t.Errorf("no magnet was available, so none may be advertised: %q", got)
	}

	// Neither a magnet nor a detail page: the whole line has to go. A caption
	// reading "直达链接：" with nothing after it promises a link it does not have.
	if got := Render("正文\n直达链接：{torrent_text}", TemplateData{}); got != "正文" {
		t.Errorf("a link with no destination was published: %q", got)
	}
	// A line that pairs the link with other text is content, not a bare link,
	// and must be left for the operator to decide about.
	kept := Render("正文\n直达链接：{torrent_text} · {size}", TemplateData{Size: "1 GB"})
	if !strings.Contains(kept, "· 1 GB") {
		t.Errorf("a link line carrying other text was not left alone: %q", kept)
	}
}

// {torrent_label} names the destination in words, the way the 1.3.0 and 1.3.1
// presets used it. It is kept so a caption written against those releases keeps
// rendering, and it has to agree with {torrent_text} about the destination.
func TestRenderTorrentLabelNamesTheDestination(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:DEADBEEF&tr=udp%3A%2F%2Ft.example%3A6969"
	got := Render("{torrent_label}|{torrent_text}", TemplateData{
		Magnet: magnet,
	})
	if want := "种子链接|<code>" + escape(magnet) + `</code>`; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	if got := Render("{torrent_label}", TemplateData{URL: "https://ext.to/y-11/"}); got != "详情页" {
		t.Errorf("without a magnet the label should say 详情页: %q", got)
	}
	// With neither, the label must not name a destination that does not exist.
	if got := Render("{torrent_label}", TemplateData{}); got != "" {
		t.Errorf("a label with nothing to point at was published: %q", got)
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

	// The 1.3.0 / 1.3.1 presets linked the torrent but labelled it with words.
	// An install that never touched them carries them verbatim and must move
	// onto the wording that prints the link.
	oldDefault := "名称：{title}\n分类：{category}\n大小：{size} · {files} 个文件\n" +
		"做种：{seeds} · 下载：{leeches}\n发布：{age}\n\n" +
		`<a href="{torrent_url}">{torrent_label}</a>`
	if got := migrateLegacyTemplate(oldDefault); got != DefaultTemplate {
		t.Errorf("the 1.3.1 default preset was not migrated:\n%q", got)
	}
	oldTmdb131 := "片名：{tmdb_title}{season_label}\n年份：{tmdb_year}\nTMDB：{tmdb_ref}\n" +
		"分类：{category_tmdb}\n\n{title}\n简介：{tmdb_overview}\n分享：{uploader}\n大小：{size}\n\n" +
		`<a href="{torrent_url}">{torrent_label}</a>`
	if got := migrateLegacyTemplate(oldTmdb131); got != TMDBTemplate {
		t.Errorf("the 1.3.1 TMDB preset was not migrated:\n%q", got)
	}

	// The 1.3.2 / 1.3.3 presets printed the link but wrapped it in an anchor,
	// and Telegram drops a magnet anchor: the caption looked like it pointed
	// somewhere and did not. An install carrying them verbatim must move on.
	oldDefault133 := "名称：{title}\n分类：{category}\n大小：{size} · {files} 个文件\n" +
		"做种：{seeds} · 下载：{leeches}\n发布：{age}\n\n" +
		`<a href="{torrent_url}">{torrent_text}</a>`
	if got := migrateLegacyTemplate(oldDefault133); got != DefaultTemplate {
		t.Errorf("the 1.3.2 default preset was not migrated:\n%q", got)
	}
	oldTmdb133 := "片名：{tmdb_title}{season_label}\n年份：{tmdb_year}\nTMDB：{tmdb_ref}\n" +
		"分类：{category_tmdb}\n\n{title}\n简介：{tmdb_overview}\n分享：{uploader}\n大小：{size}\n\n" +
		`<a href="{torrent_url}">{torrent_text}</a>`
	if got := migrateLegacyTemplate(oldTmdb133); got != TMDBTemplate {
		t.Errorf("the 1.3.2 TMDB preset was not migrated:\n%q", got)
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

// The shipped presets must print the torrent rather than the tracker page, and
// must not leave a dead link when no magnet could be resolved. The link is
// printed as text, so a forwarded caption still carries something usable.
//
// They must not wrap it in an anchor: Telegram accepts no magnet: URL as a
// hyperlink, so the anchor would be dropped and the caption would only look like
// it points somewhere.
func TestShippedTemplatesLinkTheTorrent(t *testing.T) {
	for name, tpl := range map[string]string{"DefaultTemplate": DefaultTemplate, "TMDBTemplate": TMDBTemplate} {
		if !strings.Contains(tpl, "{torrent_text}") {
			t.Errorf("%s does not print the link, so a forwarded caption loses it", name)
		}
		if strings.Contains(tpl, "{torrent_label}") {
			t.Errorf("%s still shows the words instead of the link", name)
		}
		if strings.Contains(tpl, `href="{torrent_url}"`) || strings.Contains(tpl, `href="{url}"`) {
			t.Errorf("%s wraps the torrent in an anchor, which Telegram drops for a magnet", name)
		}
		out := Render(tpl, TemplateData{
			Title: "某片 2026", Size: "1.43 GB", Magnet: "magnet:?xt=urn:btih:DEADBEEF",
			TMDBTitle: "某片", TMDBYear: 2026, TMDBID: 287994, TMDBType: "tv",
		})
		if want := `<code>magnet:?xt=urn:btih:DEADBEEF</code>`; !strings.Contains(out, want) {
			t.Errorf("%s did not render the magnet as a copyable span:\n%s", name, out)
		}
	}
}

// Telegram rejects a photo caption longer than 1024 characters, and the TMDB
// preset carries the overview, which is the largest value in it. The budget is
// measured rather than assumed, because the preset is written once and has to
// survive the longest values the fields can hold.
//
// The limit is counted over the visible text: a link's href is markup, not
// text, and entities count as the character they render rather than as the
// digits that spell them. Both were measured against the live Bot API: 1024
// runes of visible text is accepted and 1025 is rejected, while 1024 "&amp;"
// (5120 raw characters) is accepted and "<b>" costs nothing.
//
// visibleLen is the renderer's own count, and this test checks the caption
// against that same measure. What keeps the two honest is the probe run against
// the real Bot API, not this test alone.
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
		if visible := visibleLen(out); visible > captionLimit {
			t.Errorf("%s renders %d visible runes, over Telegram's 1024 caption limit:\n%s",
				name, visible, out)
		}
		// The link is printed, and printed with its trackers: dropping them
		// leaves the info hash, which a cloud download service cannot use.
		if !strings.Contains(out, "<code>magnet:?xt=urn:btih:"+strings.Repeat("a", 40)+"&amp;tr=") {
			t.Errorf("%s did not print the magnet with its trackers:\n%s", name, out)
		}
	}
}

// The whole magnet is printed whenever the caption fits, because its trackers
// are what let a download service find the files. A magnet without one is the
// info hash alone: a torrent client can still open it, but the service shows
// the hash where the file names should be.
func TestWholeMagnetIsPrintedWhenItFits(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:cafebabe" + strings.Repeat("&tr=udp://tracker.example:6969/announce", 3)
	out := Render(TMDBTemplate, TemplateData{
		Title: "某片", Magnet: magnet, URL: "https://ext.to/x-1/",
		TMDBTitle: "某片", TMDBYear: 2026, TMDBID: 1, TMDBType: "movie",
	})
	if !strings.Contains(out, escape(magnet)) {
		t.Errorf("the magnet was not printed in full:\n%s", out)
	}
	// The link is printed once, as the text a reader copies: there is no anchor
	// for it to be duplicated into, because Telegram drops a magnet anchor.
	if n := strings.Count(out, "&amp;tr="); n != 3 {
		t.Errorf("printed %d tracker references, want 3 (once, as text):\n%s", n, out)
	}
}

// The synopsis is cut only as far as the link needs, and a link that fits with
// no synopsis at all is still printed whole rather than trimmed.
func TestSynopsisYieldsBeforeTheLinkIsTrimmed(t *testing.T) {
	// Room for the link plus part of the synopsis, so the cut has to happen.
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) +
		strings.Repeat("&tr=udp://tracker.example:6969/announce", 11)
	out := Render(TMDBTemplate, TemplateData{
		Title: "某片 2026", Magnet: magnet, URL: "https://ext.to/x-1/",
		TMDBTitle: "某片", TMDBYear: 2026, TMDBID: 1, TMDBType: "movie",
		TMDBOverview: strings.Repeat("简介正文", 300),
		RuleCategory: "欧美电影",
	})
	if visible := visibleLen(out); visible > captionLimit {
		t.Fatalf("caption is %d visible runes, over the limit:\n%s", visible, out)
	}
	if !strings.Contains(out, escape(magnet)) {
		t.Errorf("the magnet was trimmed although cutting the synopsis would have fit it:\n%s", out)
	}
	if !strings.Contains(out, "简介：") {
		t.Errorf("the synopsis was given up entirely rather than shortened:\n%s", out)
	}

	// A link long enough that only a caption with no synopsis can hold it: the
	// whole link still survives, because the synopsis is what gives way.
	long := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) +
		strings.Repeat("&tr=udp://tracker.example:6969/announce", 20)
	out = Render(TMDBTemplate, TemplateData{
		Title: "某片 2026", Magnet: long, URL: "https://ext.to/x-1/",
		TMDBTitle: "某片", TMDBYear: 2026, TMDBID: 1, TMDBType: "movie",
		TMDBOverview: strings.Repeat("简介正文", 300), RuleCategory: "欧美电影",
	})
	if visible := visibleLen(out); visible > captionLimit {
		t.Fatalf("caption is %d visible runes, over the limit:\n%s", visible, out)
	}
	if !strings.Contains(out, escape(long)) {
		t.Errorf("the whole link should survive with the synopsis dropped:\n%s", out)
	}
}

// A magnet so long that not even an empty caption has room for it: the link is
// trimmed, but never below the info hash, because a link without it names
// nothing.
func TestOverlongMagnetIsTrimmedButKeepsTheInfoHash(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) +
		strings.Repeat("&tr=udp://tracker.example:6969/announce", 26)
	out := Render(TMDBTemplate, TemplateData{
		Title: "某片 2026", Magnet: magnet, URL: "https://ext.to/x-1/",
		TMDBTitle: "某片", TMDBYear: 2026, TMDBID: 1, TMDBType: "movie",
		TMDBOverview: strings.Repeat("简介正文", 300), RuleCategory: "欧美电影",
	})
	if visible := visibleLen(out); visible > captionLimit {
		t.Fatalf("caption is %d visible runes, over the limit:\n%s", visible, out)
	}
	if !strings.Contains(out, "magnet:?xt=urn:btih:"+strings.Repeat("a", 40)) {
		t.Errorf("the info hash was dropped from the link:\n%s", out)
	}
	if !strings.Contains(out, "&amp;tr=") {
		t.Errorf("every tracker was dropped although room was left for some:\n%s", out)
	}
}

// With no synopsis to give up the link has to shrink, but it stays a link:
// whole tracker parameters go from the end, and the info hash -- the part that
// names the torrent -- is always kept.
func TestTrimmedMagnetKeepsTheInfoHashAndWholeParams(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:cafebabe" + strings.Repeat("&tr=udp://tracker.example:6969/announce", 30)
	got := trimMagnet(magnet, 200)
	if visibleLen(got) > 200 {
		t.Errorf("trimMagnet returned %d characters, want at most 200: %q", visibleLen(got), got)
	}
	if !strings.HasPrefix(got, "magnet:?xt=urn:btih:cafebabe") {
		t.Errorf("the info hash was cut off: %q", got)
	}
	if strings.HasSuffix(got, "announ") || strings.Contains(got, "&&") {
		t.Errorf("a parameter was cut mid-way: %q", got)
	}
	for _, p := range strings.Split(got, "&")[1:] {
		if p != "tr=udp://tracker.example:6969/announce" {
			t.Errorf("unexpected parameter %q in %q", p, got)
		}
	}
	// A link that fits is returned untouched, trackers and all.
	if got := trimMagnet(magnet, visibleLen(magnet)); got != magnet {
		t.Errorf("trimMagnet changed a link that fits:\n%q", got)
	}
	// No parameter boundary to cut at: the link is cut without an ellipsis, so
	// it does not look like a link it is not.
	long := "magnet:?xt=" + strings.Repeat("a", 300)
	if got := trimMagnet(long, 50); visibleLen(got) != 50 || strings.Contains(got, "…") {
		t.Errorf("trimMagnet with no boundary = %q", got)
	}
}

// A caption with no link and no room is still bounded: the renderer must not
// publish something Telegram will reject.
func TestCaptionWithoutALinkIsStillBounded(t *testing.T) {
	out := Render(TMDBTemplate, TemplateData{
		Title: strings.Repeat("长标题 ", 400), TMDBTitle: strings.Repeat("片名", 100),
		TMDBID: 1, TMDBType: "movie", TMDBOverview: strings.Repeat("简介", 500),
		URL: "https://ext.to/x-1/",
	})
	if visible := visibleLen(out); visible > captionLimit {
		t.Errorf("caption is %d visible runes, over the limit:\n%s", visible, out)
	}
	if strings.Contains(out, torrentTextSentinel) {
		t.Errorf("the sentinel leaked into the caption: %q", out)
	}
}

// The sentinel must never reach a published caption, in any of the paths.
func TestSentinelNeverLeaks(t *testing.T) {
	cases := []TemplateData{
		{Title: "t", Magnet: "magnet:?xt=urn:btih:AB", URL: "https://ext.to/x-1/"},
		{Title: "t", URL: "https://ext.to/x-1/"},
		{Title: "t"},
		{Title: "t", Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40) +
			strings.Repeat("&tr=udp://tracker.example:6969/announce", 30)},
	}
	tpls := []string{DefaultTemplate, TMDBTemplate, "<a href=\"{torrent_url}\">{torrent_text}</a>", "{torrent_text}"}
	for i, d := range cases {
		for _, tpl := range tpls {
			out := Render(tpl, d)
			if strings.Contains(out, torrentTextSentinel) || strings.ContainsRune(out, 0) {
				t.Errorf("case %d, template %q: sentinel leaked: %q", i, tpl, out)
			}
		}
	}
}

// clampVisible is the last line of defence, so it is pinned on its own: it has
// to count the characters Telegram counts, cut between them rather than inside
// them, and never leave half an entity behind.
func TestClampVisible(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"under the limit", "short", 10, "short"},
		{"exactly at it", "abcde", 5, "abcde"},
		{"ascii cut", "abcdefghij", 5, "abcde"},
		// CJK counts by character: cutting by byte would keep a third of it.
		{"cjk cut", "字字字字字字字字字字", 4, "字字字字"},
		{"cjk kept whole", "字字", 2, "字字"},
		// A tag is free and must not be counted, nor cut into.
		{"tag is free", "<b>abcdef</b>", 3, "<b>abc"},
		{"tag survives the cut", "<b>abcdef</b>", 6, "<b>abcdef</b>"},
		// An entity is one character, and must not be split.
		{"entity cut", "&amp;&amp;&amp;&amp;", 2, "&amp;&amp;"},
		{"entity whole", "&amp;", 1, "&amp;"},
		{"entity then text", "&amp;abc", 2, "&amp;a"},
		{"zero limit", "abc", 0, ""},
	}
	for _, c := range cases {
		got := clampVisible(c.in, c.limit)
		if got != c.want {
			t.Errorf("%s: clampVisible(%q, %d) = %q, want %q",
				c.name, c.in, c.limit, got, c.want)
		}
		if visibleLen(got) > c.limit {
			t.Errorf("%s: clampVisible returned %d characters, over the %d limit: %q",
				c.name, visibleLen(got), c.limit, got)
		}
	}
	// Whatever it returns has to stay valid UTF-8 and keep the entities whole.
	long := strings.Repeat("字&amp;<b>", 100)
	for limit := 0; limit <= 60; limit++ {
		got := clampVisible(long, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("limit %d produced invalid UTF-8: %q", limit, got)
		}
		if strings.Count(got, "&") != strings.Count(got, ";") && !strings.HasPrefix(long[len(got):], "amp;") {
			t.Fatalf("limit %d left half an entity: %q", limit, got)
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
