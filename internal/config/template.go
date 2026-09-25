package config

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/haoyu010/ext.to/internal/media"
)

// captionLimit is the number of characters Telegram accepts in a photo
// caption. It is counted over the text a reader sees, and it is measured rather
// than assumed: against the live Bot API, 1024 runes is accepted and 1025 is
// rejected with "message caption is too long", while the tags and entities that
// carry the markup cost nothing -- "<b>" is free and "&amp;" is one character,
// not five.
//
// The photo limit is used even for a caption that will be sent as plain text,
// where the limit is 4096: rendering has no way to know which one it is, and a
// caption built for the smaller limit is valid for both.
const captionLimit = 1024

// torrentTextSentinel stands where the printable link goes while the caption is
// being measured. The link's length decides how much of it can be printed, and
// that cannot be known until the rest of the caption has been rendered, so the
// substitution is done in two passes: everything else first, then the link into
// whatever room is left.
//
// A NUL byte cannot appear in a caption otherwise: no placeholder produces one,
// and Telegram would not accept it in a value.
const torrentTextSentinel = "\x00link\x00"

// reMarkup matches HTML tags, which Telegram does not count against the caption
// limit. Anchors are the reason it matters: a link's destination is markup and
// free, while its text is not.
var reMarkup = regexp.MustCompile(`<[^>]*>`)

// overviewCap is the longest synopsis worth publishing, and overviewStep the
// granularity it is given up in. Re-rendering the caption once per step is
// cheap next to publishing a truncated link, and a coarse step keeps the number
// of passes small.
const (
	overviewCap  = 320
	overviewStep = 40
)

// DefaultTemplate is the caption used for new installs. It is rendered as
// Telegram HTML, so only <b>, <i>, <a>, <code> and <pre> tags are allowed.
//
// The fields are written as labelled lines rather than as icon-separated ones,
// because a label survives a value going missing: a line that reads "分类："
// with nothing after it is still readable, while a row of two values joined by
// a separator leaves a stray bullet when one of them is empty.
//
// The single link is the torrent itself, not the tracker's detail page. The
// detail page is a web page nobody wants mid-download, and {torrent_url}
// already falls back to it when no magnet could be resolved, so the line never
// becomes a dead link.
//
// Its text is the link itself rather than the words "种子链接": a reader
// copies a caption by copying text, and the words are worth nothing once the
// message has been forwarded somewhere the anchor no longer renders.
//
// Printing the link means carrying its trackers, because a magnet without one
// is the info hash and nothing else: a torrent client can still use it, but a
// cloud download service cannot, and shows the hash instead of the files. The
// trackers are only given up when the caption would otherwise be rejected, and
// the overview is given up first.
const DefaultTemplate = `名称：{title}
分类：{category}
大小：{size} · {files} 个文件
做种：{seeds} · 下载：{leeches}
发布：{age}

<a href="{torrent_url}">{torrent_text}</a>`

// TMDBTemplate is an optional caption preset that leads with the matched TMDB
// entry. It is offered in the dashboard rather than applied by default, because
// it only renders well once a TMDB key is configured.
//
// The TMDB reference is plain text rather than a link: the entry's own page is
// a detour for a reader who came for the torrent, and the type and number
// already identify it unambiguously ("tv/287994"). The one link in the caption
// is the torrent.
//
// {title} on its own line is the release name as published. It is kept beside
// the TMDB fields because the two answer different questions: the TMDB lines
// say which work this is, and the release name says which rip it is, down to
// the resolution and the group.
const TMDBTemplate = `片名：{tmdb_title}{season_label}
年份：{tmdb_year}
TMDB：{tmdb_ref}
分类：{category_tmdb}

{title}
简介：{tmdb_overview}
分享：{uploader}
大小：{size}

<a href="{torrent_url}">{torrent_text}</a>`

// legacyTemplates are the presets this project shipped before the torrent link
// was introduced, kept so an existing install can be moved onto the current
// ones.
//
// They are matched byte for byte, so a template the operator has edited is
// left exactly as written: only an install still carrying a preset verbatim is
// migrated. The migration is needed because the old pairs link the tracker's
// detail page and never reference {magnet}, so an install that never touched
// them has no way to learn that a torrent link exists.
//
// The 1.3.0 and 1.3.1 presets are here for the same reason one release later:
// they linked the torrent but labelled it "种子链接", and an install that never
// edited its caption would otherwise keep showing the words instead of the
// link. The wording of the migration is the same: only a verbatim preset moves.
//
// The two oldest pairs also carry the presets that replaced them, so an
// install coming from 1.2.x lands on the 1.3.2 wording in one step.
var legacyTemplates = map[string]string{
	`<b>{title}</b>

📁 {category}
💾 {size} · 📄 {files} files
🌱 {seeds} seeders · {leeches} leechers
🕐 {age}

<a href="{url}">Open on ext.to</a>`: DefaultTemplate,
	`<b>{tmdb_title}</b>{season_label}{tmdb_year_paren}
⭐ {tmdb_rating}/10 · {tmdb_votes} votes

📁 {category_tmdb}
💾 {size} · 📄 {files} files
🌱 {seeds} seeders · {leeches} leechers
🕐 {age}

<a href="{tmdb_url}">TMDB</a> · <a href="{url}">ext.to</a>`: TMDBTemplate,
	`名称：{title}
分类：{category}
大小：{size} · {files} 个文件
做种：{seeds} · 下载：{leeches}
发布：{age}

<a href="{torrent_url}">{torrent_label}</a>`: DefaultTemplate,
	`片名：{tmdb_title}{season_label}
年份：{tmdb_year}
TMDB：{tmdb_ref}
分类：{category_tmdb}

{title}
简介：{tmdb_overview}
分享：{uploader}
大小：{size}

<a href="{torrent_url}">{torrent_label}</a>`: TMDBTemplate,
}

// migrateLegacyTemplate upgrades a template still held verbatim from an
// earlier release, and returns anything else unchanged.
func migrateLegacyTemplate(tpl string) string {
	if next, ok := legacyTemplates[tpl]; ok {
		return next
	}
	return tpl
}

// TemplateFields lists the placeholders available in a caption template. The
// descriptions are shown in the dashboard, which is Chinese, so they are
// written in Chinese too.
var TemplateFields = []struct{ Key, Desc string }{
	{"{title}", "种子原始标题"},
	{"{tmdb_title}", "TMDB 匹配到的标题，未匹配时为种子标题"},
	{"{tmdb_original_title}", "TMDB 原始语言标题"},
	{"{tmdb_year}", "TMDB 发行年份"},
	{"{tmdb_rating}", "TMDB 平均评分，例如 7.1"},
	{"{tmdb_votes}", "TMDB 评分人数"},
	{"{tmdb_url}", "TMDB 条目链接"},
	{"{tmdb_id}", "TMDB 数字编号"},
	{"{tmdb_type}", "movie 或 tv"},
	{"{tmdb_ref}", "TMDB 的「类型/编号」，例如 tv/287994；未匹配时为空"},
	{"{tmdb_overview}", "TMDB 简介，会按 Telegram 限制截断"},
	{"{category_tmdb}", "TMDB 分类规则命中的分类，例如 国漫、国产剧，未命中时为「未分类」"},
	{"{season}", "季数，例如 6；发布名未写季数时为空。TMDB 的剧集条目含全部季，季数只来自发布名"},
	{"{season_label}", "「 第 6 季」（含前导空格，便于直接接在标题后）；无季数时为空"},
	{"{tmdb_year_paren}", "「(2016)」（含括号）；TMDB 无年份时为空，不会留下空括号"},
	{"{episode}", "集数，例如 12；发布名未写集数时为空"},
	{"{category}", "分类路径，例如 Movies - Highres Movies"},
	{"{size}", "可读体积，例如 1.43 GB"},
	{"{files}", "种子内文件数"},
	{"{seeds}", "做种人数"},
	{"{leeches}", "下载人数"},
	{"{age}", "ext.to 给出的发布时间，例如 5 minutes ago"},
	{"{source}", "来源站点，例如 DHT 或 UIndex"},
	{"{uploader}", "ext.to 给出的发布者"},
	{"{url}", "种子详情页的完整链接"},
	{"{magnet}", "磁力链接，取不到时为空"},
	{"{torrent_url}", "种子链接：磁力链接，取不到时退回详情页链接，永远可用"},
	{"{torrent_text}", "配合 {torrent_url} 显示的文字：整条磁力链接本身（含 tracker），放不下时先缩简介、再从尾部丢 tracker，必要时退回详情页链接"},
	{"{torrent_label}", "指向同一个去向的文字版：「种子链接」或「详情页」，保留给旧模板"},
	{"{id}", "ext.to 种子编号"},
}

// TemplateData is the value set substituted into a caption template.
type TemplateData struct {
	Title    string
	Category string
	Size     string
	Files    int
	Seeds    int
	Leeches  int
	Age      string
	Source   string
	Uploader string
	URL      string
	Magnet   string
	ID       int

	// TMDB fields are empty when the release was not matched.
	TMDBTitle         string
	TMDBOriginalTitle string
	TMDBYear          int
	TMDBRating        float64
	TMDBVotes         int
	TMDBURL           string
	TMDBID            int
	TMDBType          string
	// TMDBRef is the type and id as one token, "tv/287994". It is separate from
	// the two fields it is built from so a caption can print it as text without
	// the separator and the empty case becoming the template's problem: a
	// template written as "{tmdb_type}/{tmdb_id}" renders "/0" for a release
	// that matched nothing.
	TMDBRef      string
	TMDBOverview string
	// Season and Episode are the numbers the release name stated, and are zero
	// when it stated none. They come from the release name rather than from
	// TMDB, because a TMDB series entry covers every season: the entry says
	// which work a release is, and only the release name says which instalment.
	Season  int
	Episode int
	// RuleCategory is the name the classification rules produced from the TMDB
	// match, such as 国漫. It is distinct from Category, which is the tracker's
	// own path and says nothing about region or medium.
	RuleCategory string
}

// Render substitutes placeholders in tpl. Values are HTML-escaped because
// Telegram parses captions as HTML; callers embedding markup must escape it
// themselves.
func Render(tpl string, d TemplateData) string {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultTemplate
	}
	// Unmatched releases must not render dangling markup, so the TMDB
	// placeholders degrade to their torrent equivalents.
	tmdbTitle := d.TMDBTitle
	if tmdbTitle == "" {
		tmdbTitle = d.Title
	}
	tmdbYear := ""
	if d.TMDBYear != 0 {
		tmdbYear = fmt.Sprint(d.TMDBYear)
	}
	tmdbRating := ""
	if d.TMDBID != 0 {
		tmdbRating = fmt.Sprintf("%.1f", d.TMDBRating)
	}
	tmdbVotes := ""
	if d.TMDBID != 0 {
		tmdbVotes = humanCount(d.TMDBVotes)
	}
	tmdbURL := d.TMDBURL
	if tmdbURL == "" {
		tmdbURL = d.URL
	}
	// TMDBRef is the type and id as one token, so a caption can print the
	// reference as plain text. Building it here rather than in the template
	// keeps the empty case out of the template: "{tmdb_type}/{tmdb_id}" would
	// render "/0" for a release that matched nothing.
	tmdbRef := ""
	if d.TMDBID != 0 {
		tmdbRef = fmt.Sprintf("%s/%d", d.TMDBType, d.TMDBID)
	}
	// One link serves both cases. A magnet is what a reader actually wants, and
	// the detail page is the honest fallback when none could be resolved: the
	// magnet endpoint is per-page and a failure there must not leave a dead
	// link in a published post.
	//
	// The two placeholders differ in what the caption shows, not in where the
	// link goes. {torrent_text} prints the link itself, because a reader who
	// forwards a caption carries the text and not the anchor; {torrent_label}
	// names the destination in words and is kept for templates written against
	// the earlier releases.
	torrentURL, torrentText, torrentLabel := d.Magnet, d.Magnet, ""
	if torrentURL == "" {
		torrentURL, torrentText, torrentLabel = d.URL, d.URL, "详情页"
	} else {
		torrentLabel = "种子链接"
	}
	// Like the TMDB title, the classified name degrades to the tracker's own
	// category rather than rendering an empty value, so a template using it
	// never leaves a dangling label.
	ruleCategory := d.RuleCategory
	if ruleCategory == "" {
		ruleCategory = d.Category
	}
	// A release with no season is a film or a complete-series pack, so the
	// placeholders render empty rather than as a misleading "0".
	season, episode := "", ""
	if d.Season > 0 {
		season = fmt.Sprint(d.Season)
	}
	if d.Episode > 0 {
		episode = fmt.Sprint(d.Episode)
	}
	// The label carries its own leading space so a template can put it straight
	// after the title without leaving a gap when there is no season: the
	// preset renders "<b>名称</b> 第 6 季 (2016)" and, for a film,
	// "<b>名称</b> (2023)" rather than a double space or a stray separator.
	//
	// It is suppressed when the title already writes the season out, which is
	// what the label is defined against: TMDB files some seasons as their own
	// entry named after them ("毛骗 第二季" beside "毛骗"), so appending would
	// render "毛骗 第二季 第 2 季". The season itself is still available as
	// {season}, because a template may want the number on its own.
	seasonLabel := ""
	if season != "" && !media.StatesSeason(tmdbTitle, d.Season) {
		seasonLabel = " 第 " + season + " 季"
	}
	// The year is bracketed here rather than in the template so that a missing
	// year leaves nothing at all. Some TMDB series entries carry an empty
	// first_air_date ("毛骗 第二季 (2011)" does), and a template written as
	// "({tmdb_year})" renders "()" for them.
	yearParen := ""
	if tmdbYear != "" {
		yearParen = " (" + tmdbYear + ")"
	}
	// render builds the caption with the printable link left as a sentinel and
	// the synopsis cut to ovCap, because the room the link needs depends on how
	// much of the synopsis is kept. Rendering it in passes keeps the sentinel
	// and the empty-value rules working together: a line whose only value is the
	// sentinel is not empty yet, so it survives until the link is known.
	render := func(ovCap int) string {
		// A budget of zero means the synopsis is given up entirely, which is
		// not the same as cutting it to nothing: truncateRunes would leave the
		// ellipsis behind, and "简介：…" is worse than dropping the line.
		overview := ""
		if ovCap > 0 {
			overview = escape(truncateRunes(d.TMDBOverview, ovCap))
		}
		rep := strings.NewReplacer(
			"{title}", escape(d.Title),
			"{tmdb_title}", escape(tmdbTitle),
			"{tmdb_original_title}", escape(d.TMDBOriginalTitle),
			"{tmdb_year}", escape(tmdbYear),
			"{tmdb_rating}", escape(tmdbRating),
			"{tmdb_votes}", escape(tmdbVotes),
			"{tmdb_url}", escape(tmdbURL),
			"{tmdb_id}", fmt.Sprint(d.TMDBID),
			"{tmdb_type}", escape(d.TMDBType),
			"{tmdb_ref}", escape(tmdbRef),
			"{tmdb_overview}", overview,
			"{category_tmdb}", escape(ruleCategory),
			"{season}", escape(season),
			"{season_label}", escape(seasonLabel),
			"{tmdb_year_paren}", escape(yearParen),
			"{episode}", escape(episode),
			"{category}", escape(d.Category),
			"{size}", escape(d.Size),
			"{files}", fmt.Sprint(d.Files),
			"{seeds}", fmt.Sprint(d.Seeds),
			"{leeches}", fmt.Sprint(d.Leeches),
			"{age}", escape(d.Age),
			"{source}", escape(d.Source),
			"{uploader}", escape(d.Uploader),
			"{url}", escape(d.URL),
			"{magnet}", escape(d.Magnet),
			"{torrent_url}", escape(torrentURL),
			"{torrent_text}", torrentTextSentinel,
			"{torrent_label}", escape(torrentLabel),
			"{id}", fmt.Sprint(d.ID),
		)
		return dropEmptyValueLines(rep.Replace(tpl))
	}

	// The whole magnet is what a reader needs, because its trackers are what let
	// a download service find the files. A magnet without one is an info hash
	// and nothing else: a torrent client can still use it, but a cloud download
	// service shows the hash instead of the files. So the link is given the room
	// it needs and the synopsis yields, largest first, until the caption fits.
	var out string
	switch {
	// Neither a magnet nor a detail page, so the sentinel stands for nothing.
	// Filling it and re-running the empty-value rule drops the link line rather
	// than publishing a dead link.
	case torrentText == "":
		out = dropEmptyValueLines(strings.ReplaceAll(render(overviewCap), torrentTextSentinel, ""))

	// A template that never mentions the link is not charged for one: the
	// sentinel only appears where the operator wrote {torrent_text}.
	case !strings.Contains(render(overviewCap), torrentTextSentinel):
		out = render(overviewCap)

	default:
		out = ""
		for _, ovCap := range overviewBudgets() {
			caption := render(ovCap)
			rest := strings.ReplaceAll(caption, torrentTextSentinel, "")
			if room := captionLimit - visibleLen(rest); visibleLen(torrentText) <= room {
				out = strings.ReplaceAll(caption, torrentTextSentinel, escape(torrentText))
				break
			}
		}
		// Even with no synopsis the caption is over the limit, so the link has
		// to give up trackers. The info hash is kept: it is the part that names
		// the torrent, and a link without it means nothing at all.
		if out == "" {
			caption := render(0)
			room := captionLimit - visibleLen(strings.ReplaceAll(caption, torrentTextSentinel, ""))
			if room < 0 {
				room = 0
			}
			out = strings.ReplaceAll(caption, torrentTextSentinel, escape(trimMagnet(torrentText, room)))
		}
	}
	return clampVisible(out, captionLimit)
}

// overviewBudgets lists the synopsis lengths to try, longest first. Each step is
// the next concession: the first one that lets the whole link through wins, so
// the synopsis is only ever cut as far as the link actually needs.
func overviewBudgets() []int {
	out := make([]int, 0, overviewCap/overviewStep+1)
	for n := overviewCap; n > 0; n -= overviewStep {
		out = append(out, n)
	}
	return append(out, 0)
}

// trimMagnet shortens a magnet link to at most room visible characters,
// preferring to keep whole tracker parameters.
//
// Only the parameters that fit are kept, so the result stays a link a client
// can act on rather than a string cut mid-URL. The info hash is always first
// and always kept: it is the part that names the torrent, and without it the
// link means nothing at all.
func trimMagnet(magnet string, room int) string {
	if visibleLen(magnet) <= room {
		return magnet
	}
	head, _, ok := strings.Cut(magnet, "&")
	if !ok {
		// No parameter boundary to cut at, so there is nothing to keep but the
		// beginning. It is cut without an ellipsis: a link is not prose, and a
		// trailing "…" would only make it look like a link it is not.
		r := []rune(magnet)
		if room < 1 {
			return ""
		}
		return string(r[:room])
	}
	out := head
	for _, param := range strings.Split(magnet[len(head)+1:], "&") {
		if param == "" {
			continue
		}
		if visibleLen(out)+1+visibleLen(param) > room {
			break
		}
		out += "&" + param
	}
	return out
}

// visibleLen counts the characters Telegram counts: the text a reader sees,
// with the markup tags removed and the entities decoded.
//
// It is deliberately an over-count where the two could differ, because being
// one character over loses the whole message. "&amp;" is counted as the single
// character it renders, and anything that looks like a tag is dropped, which
// errs on the side of a shorter caption.
func visibleLen(s string) int {
	s = reMarkup.ReplaceAllString(s, "")
	s = strings.NewReplacer(
		"&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#34;", `"`, "&#39;", "'",
		"&amp;", "&",
	).Replace(s)
	return utf8.RuneCountInString(s)
}

// clampVisible cuts s so that its visible text is at most limit characters.
//
// It is the last line of defence, for a caption that overflows even after the
// synopsis has been given up and the link has been trimmed to its info hash.
// Telegram rejects such a caption outright, and the forwarder records a
// rejection as a permanent failure rather than retrying it, so a caption that
// cannot be sent is a post that is lost -- a truncated one at least arrives.
//
// The cut is made between characters, never inside a tag or an entity, so it
// cannot turn a value into something else or leave half of "&amp;" behind, and
// it never splits a multi-byte character. It can leave a tag unclosed, which
// Telegram rejects in turn; the send path already handles that by retrying as
// plain text.
func clampVisible(s string, limit int) string {
	used, i := 0, 0
	for i < len(s) && used < limit {
		if s[i] == '<' {
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				break
			}
			i += end + 1
			continue
		}
		step := 1
		if s[i] == '&' {
			if n := entityLen(s[i:]); n > 0 {
				step = n
			}
		}
		// The text between markup is cut by character, not by byte: counting
		// bytes would spend the whole budget in a third of a CJK caption and
		// cut the rest away.
		if step == 1 {
			_, size := utf8.DecodeRuneInString(s[i:])
			if size > 1 {
				step = size
			}
		}
		used++
		i += step
	}
	if i >= len(s) {
		return s
	}
	// Anything that is markup at the cut point is kept, so a tag opened before
	// it is still closed after it. A caption that ends inside <b> is rejected by
	// Telegram, and losing the message would be worse than the overshoot: a tag
	// costs nothing against the limit.
	out := s[:i]
	for i < len(s) && s[i] == '<' {
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			break
		}
		out += s[i : i+end+1]
		i += end + 1
	}
	return out
}

// entityLen returns the byte length of the HTML entity at the start of s, or
// zero when s does not begin with one. It exists to keep clampVisible from
// cutting an entity in half.
func entityLen(s string) int {
	end := strings.IndexByte(s, ';')
	if end < 0 || end > 10 {
		return 0
	}
	name := s[1:end]
	if strings.HasPrefix(name, "#x") || strings.HasPrefix(name, "#X") {
		name = name[2:]
	} else if strings.HasPrefix(name, "#") {
		name = name[1:]
	}
	if name == "" {
		return 0
	}
	for _, r := range name {
		isDigit := r >= '0' && r <= '9'
		isHex := (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isDigit && !isHex && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') {
			return 0
		}
	}
	return end + 1
}

// dropEmptyValueLines removes lines that ended up promising something they do
// not deliver: a bare label such as "年份：" when the matched entry carries no
// air date, and a link with nothing to point at.
//
// A labelled caption is written once and rendered for every release, but not
// every field exists for every one: some TMDB series entries have no
// first_air_date, and an unmatched release has no TMDB fields at all. Leaving
// the label behind would publish a line that promises a value and delivers
// none, so the line is dropped instead. A link is the same shape of problem:
// the magnet is resolved per page and can fail, so a caption whose link line
// has no destination must not be published as a dead link.
//
// Only a line that is nothing but a short label and a colon, or nothing but an
// anchor with an empty href, is removed. That keeps the rule from touching
// prose or an intentional value such as "大小：NG".
func dropEmptyValueLines(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, line := range lines {
		if isEmptyLabelLine(line) || isEmptyLinkLine(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isEmptyLinkLine reports whether one line is an anchor with no destination.
func isEmptyLinkLine(line string) bool {
	t := strings.TrimSpace(line)
	if len(t) < len(`<a href=""></a>`) {
		return false
	}
	if !strings.HasPrefix(t, `<a href="">`) {
		return false
	}
	rest := strings.TrimPrefix(t, `<a href="">`)
	end := strings.Index(rest, "</a>")
	if end < 0 {
		// Unclosed markup is Telegram's to reject, not this rule's to hide.
		return false
	}
	// Anything after the closing tag is content in its own right.
	return strings.TrimSpace(rest[end+len("</a>"):]) == ""
}

// isEmptyLabelLine reports whether one line is a label with no value after it.
func isEmptyLabelLine(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasSuffix(t, "：") && !strings.HasSuffix(t, ":") {
		return false
	}
	label := strings.TrimSuffix(strings.TrimSuffix(t, "："), ":")
	if label == "" {
		return false
	}
	// A label is a short word, and markup is not part of one. Anything longer
	// is prose that happens to end in a colon and must be left alone.
	if n := len([]rune(label)); n > 12 {
		return false
	}
	return !strings.ContainsAny(label, "<>")
}

// humanCount renders a vote count compactly, for example 533052 -> "533k".
func humanCount(n int) string {
	switch {
	case n <= 0:
		return ""
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	}
}

// truncateRunes cuts s to at most n runes, appending an ellipsis when it was
// shortened. Counting runes rather than bytes keeps multi-byte titles intact.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

func escape(s string) string {
	// Telegram only recognises & < > as entities, so escaping everything and
	// then decoding quotes keeps the text predictable.
	return html.EscapeString(s)
}
