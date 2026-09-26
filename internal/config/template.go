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

// magnetUnsafe lists the characters that cannot appear unescaped inside a URI.
//
// They matter because ext.to puts the release name straight into the magnet's
// dn parameter, and a release name is full of them: "The Taking of Tiger
// Mountain 2014 m720p BluRay x264-GeneMige [ UIndex.org ]" carries spaces and
// brackets. A URI with a raw space is malformed, and a client that parses it
// strictly -- a cloud download service among them -- refuses it or stops at the
// space, which is why such a link "does not work" even though it looks right.
//
// Measured on the deployed install: all 25 of the magnets it held that carried
// a dn carried raw spaces, 13 of them also carried brackets, and one carried a
// raw "&" that split the release name into a nonsense parameter of its own.
//
// "%" is deliberately absent: it is only unsafe when it does not begin an
// escape, and encoding it would corrupt a value the site had already encoded.
const magnetUnsafe = " <>\"#[]\\^`{|}"

// copyTextLimit is the longest string a copy_text button will carry. Measured
// against the live Bot API: 256 characters is accepted, and 257 comes back as
// BUTTON_COPY_TEXT_INVALID.
//
// The limit is a third of what a caption holds, so a magnet long enough to fill
// a caption cannot be handed over whole by a button. The button therefore
// carries the longest prefix that still ends on a parameter boundary, and the
// caption carries the rest.
const copyTextLimit = 256

// CleanMagnet returns a magnet link as a legal URI, which is what a strict
// parser -- Telegram's own link detector, and the cloud download services this
// project's posts are read with -- needs to make sense of it.
//
// The repair is confined to parameter values: "&" and "=" separate parameters,
// so encoding them would merge two parameters into one. A fragment with no "="
// at all cannot be a parameter, and is dropped; that is the shape the site's own
// escaping bug produces when a release name carries a raw "&", and keeping it
// would publish a URI whose parameter names are release-name prose.
//
// A link that needs no repair is returned byte for byte, so an install whose
// magnets are already clean is unaffected.
func CleanMagnet(magnet string) string {
	const scheme = "magnet:?"
	if !strings.HasPrefix(magnet, scheme) {
		return magnet
	}
	params := strings.Split(magnet[len(scheme):], "&")
	kept := make([]string, 0, len(params))
	for _, p := range params {
		name, value, ok := strings.Cut(p, "=")
		if !ok || name == "" {
			continue
		}
		kept = append(kept, name+"="+encodeURIValue(value))
	}
	// A link whose every parameter was junk is returned as it came in: mangling
	// it further would only make the failure harder to see.
	if len(kept) == 0 {
		return magnet
	}
	return scheme + strings.Join(kept, "&")
}

// CopyMagnet returns the magnet a copy_text button should carry: clean, and cut
// to what a button accepts on a parameter boundary, so the reader gets a link a
// client can act on rather than a string cut mid-URL.
func CopyMagnet(magnet string) string {
	return trimMagnet(CleanMagnet(magnet), copyTextLimit)
}

// ShortMagnet reduces a magnet link to its info hash: "magnet:?xt=urn:btih:...".
//
// The site's own magnet carries the release name in dn and a page of trackers,
// which measures 400 to 1200 characters. That is a wall of text in a caption,
// and it is what other forwarders in this space do not print: they keep the
// info hash alone, which is 60 characters. displayMagnet is what breaks it onto two lines.
//
// The trade is trackers. A client with DHT finds peers from the hash alone, so
// a torrent client opens either form; a cloud download service does not, and
// with no tracker it shows the hash instead of the file list. Which of the two
// matters is the operator's call, so this is a setting rather than a decision,
// and ShortMagnet is the function that expresses the short one.
func ShortMagnet(magnet string) string {
	const scheme = "magnet:?"
	if !strings.HasPrefix(magnet, scheme) {
		return magnet
	}
	for _, p := range strings.Split(magnet[len(scheme):], "&") {
		name, value, ok := strings.Cut(p, "=")
		// The info hash is the parameter that names the torrent, and it is the
		// only one kept. An unrecognised shape is returned as it came in, so a
		// link this does not understand is not silently emptied.
		if ok && strings.EqualFold(name, "xt") && value != "" {
			return scheme + "xt=" + value
		}
	}
	return magnet
}

// MagnetFor returns the magnet as this install wants it printed: the site's
// whole link, or the info hash alone when short is set.
//
// Both the caption and the copy button go through here, so the two cannot
// disagree about which link a reader is given.
func MagnetFor(magnet string, short bool) string {
	magnet = CleanMagnet(magnet)
	if short {
		return ShortMagnet(magnet)
	}
	return magnet
}

// displayMagnet is the caption's shape of a short magnet. Other forwarders
// print the info hash on two lines,
//
//	magnet:?
//	xt=urn:btih:HASH
//
// so a phone can show the whole hash without scrolling sideways. The break is
// only cosmetic: it is not part of the URI. A button copies the one-line form,
// and pasting the two-line form into a single-line box drops the break, which
// leaves magnet:?xt=urn:btih:HASH either way.
//
// A link that still carries parameters is left on one line. The break exists
// to keep a 60-character hash readable, and splitting a link that is mostly
// trackers would only hide them.
func displayMagnet(magnet string) string {
	const scheme = "magnet:?"
	rest, ok := strings.CutPrefix(magnet, scheme)
	if !ok || strings.Contains(rest, "&") || !strings.HasPrefix(rest, "xt=") || len(rest) <= len("xt=") {
		return magnet
	}
	return scheme + "\n" + rest
}

// encodeURIValue percent-encodes the characters a URI cannot carry unescaped.
func encodeURIValue(v string) string {
	if !strings.ContainsAny(v, magnetUnsafe) {
		return v
	}
	var b strings.Builder
	b.Grow(len(v) + 8)
	for _, r := range v {
		if strings.ContainsRune(magnetUnsafe, r) {
			fmt.Fprintf(&b, "%%%02X", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// DefaultTemplate is the caption used for new installs. It is rendered as
// Telegram HTML, so only <b>, <i>, <a>, <code> and <pre> tags are allowed.
//
// The fields are written as labelled lines rather than as icon-separated ones,
// because a label survives a value going missing: a line that reads "分类："
// with nothing after it is still readable, while a row of two values joined by
// a separator leaves a stray bullet when one of them is empty.
//
// The link line is the torrent itself, not the tracker's detail page. The
// detail page is a web page nobody wants mid-download, and {torrent_text}
// already falls back to it when no magnet could be resolved, so the line never
// becomes a dead link.
//
// Its text is the link itself rather than the words "种子链接": a reader
// copies a caption by copying text, and the words are worth nothing once the
// message has been forwarded somewhere the anchor no longer renders.
//
// There is deliberately no <a> around it. Telegram accepts no magnet: URL as a
// hyperlink, so an anchor here would be dropped and would only make the caption
// look like it points somewhere. {torrent_text} supplies its own <code> when it
// holds a magnet, which Telegram clients copy on a tap, and none when it holds
// the detail page, whose URL Telegram linkifies by itself.
//
// The short-link switch, which is on by default, prints that link as the info
// hash alone and breaks it after the scheme, which is the two-line shape other
// forwarders use. The red bullet marks the line. Trackers are what the switch
// gives up: a torrent client finds peers from the hash, and a cloud download
// service does not. Turning the switch off prints the site's whole link instead.
const DefaultTemplate = `名称：{title}
分类：{category}
大小：{size} · {files} 个文件
做种：{seeds} · 下载：{leeches}
发布：{age}

🔴 {torrent_text}`

// TMDBTemplate is an optional caption preset that leads with the matched TMDB
// entry. It is offered in the dashboard rather than applied by default, because
// it only renders well once a TMDB key is configured.
//
// The TMDB reference is plain text rather than a link: the entry's own page is
// a detour for a reader who came for the torrent, and the type and number
// already identify it unambiguously ("tv/287994"). The one link in the caption
// is the torrent, and it is printed as text for the reason DefaultTemplate
// gives.
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

🔴 {torrent_text}`

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
//
// The 1.3.3 presets are here for the same reason one release later: they
// wrapped the torrent in an anchor, which Telegram drops for a magnet, so an
// install that never edited its caption would keep a line whose link goes
// nowhere. Again the wording of the migration is the same: only a verbatim
// preset moves.
//
// The presets that labelled the link "直达链接" are here for the same
// reason once more: an untouched caption would keep the label, and the short
// link these posts are meant to look like has none, only the red bullet.
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
	`名称：{title}
分类：{category}
大小：{size} · {files} 个文件
做种：{seeds} · 下载：{leeches}
发布：{age}

<a href="{torrent_url}">{torrent_text}</a>`: DefaultTemplate,
	`片名：{tmdb_title}{season_label}
年份：{tmdb_year}
TMDB：{tmdb_ref}
分类：{category_tmdb}

{title}
简介：{tmdb_overview}
分享：{uploader}
大小：{size}

<a href="{torrent_url}">{torrent_text}</a>`: TMDBTemplate,
	`名称：{title}
分类：{category}
大小：{size} · {files} 个文件
做种：{seeds} · 下载：{leeches}
发布：{age}

直达链接：{torrent_text}`: DefaultTemplate,
	`片名：{tmdb_title}{season_label}
年份：{tmdb_year}
TMDB：{tmdb_ref}
分类：{category_tmdb}

{title}
简介：{tmdb_overview}
分享：{uploader}
大小：{size}

直达链接：{torrent_text}`: TMDBTemplate,
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
	{"{magnet}", "磁力链接（已修正站点写在 dn 里的非法字符）；开启「只显示 info hash」时是短链接。取不到时为空"},
	{"{torrent_url}", "种子链接的去向：磁力链接，取不到时退回详情页链接，永远可用。只适合放进 href，不能直接显示"},
	{"{torrent_text}", "直接显示的种子链接：磁力会自动套上 <code>（可点击复制，转发后也不丢）；开启短链接时只印 info hash，并在 magnet:? 后换行，放不下时先缩简介、再从尾部丢 tracker，取不到磁力时退回详情页链接"},
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
	// ShortMagnet prints the magnet as its info hash alone rather than with the
	// release name and trackers the site attached. It is carried here because
	// the caption is what chooses between the two shapes, and Render has no
	// settings of its own.
	ShortMagnet bool

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
	// The magnet is repaired before anything measures or prints it, because the
	// site writes the release name into dn verbatim: a raw space there makes the
	// URI malformed, and a strict parser -- Telegram's own link detector, and
	// the cloud download services these posts are read with -- stops at it.
	magnet := CleanMagnet(d.Magnet)
	// The printable form is the whole link or the info hash alone, depending on
	// what the operator asked for. It goes through the same helper the copy
	// button uses, so the two cannot disagree about which link a reader gets.
	printable := MagnetFor(magnet, d.ShortMagnet)

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
	//
	// Telegram will not accept a magnet: hyperlink: as an entity it is rejected
	// ("Wrong port number specified in the URL") and as HTML it is dropped
	// silently, leaving the label as plain text. What it does keep is a <code>
	// span, which clients copy on a tap and which stops a fragment of the link
	// -- "UIndex.org", out of a release name in dn -- from being turned into a
	// link of its own. A magnet is therefore printed inside <code>, and the
	// anchor around it is harmless: Telegram drops the href and keeps the span.
	//
	// The detail page is the opposite case and must stay a real link, so it is
	// printed bare: Telegram linkifies a bare https URL on its own, while an
	// anchor wrapping a <code> span loses its href altogether (measured).
	var torrentURL, torrentText, torrentShown, torrentLabel, torrentHTML string
	if printable != "" {
		torrentURL, torrentText, torrentLabel = printable, printable, "种子链接"
		// The caption may break a short magnet onto two lines. The value the
		// other placeholders carry stays the one-line URI, so a button and a
		// {magnet} agree with each other and not with the line break.
		torrentShown = displayMagnet(printable)
		torrentHTML = "<code>" + escape(torrentShown) + "</code>"
	} else {
		torrentURL, torrentText, torrentShown = d.URL, d.URL, d.URL
		torrentHTML = escape(d.URL)
		if d.URL != "" {
			torrentLabel = "详情页"
		}
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
			"{magnet}", escape(printable),
			"{torrent_url}", escape(torrentURL),
			"{torrent_text}", torrentTextSentinel,
			"{torrent_label}", escape(torrentLabel),
			"{id}", fmt.Sprint(d.ID),
		)
		return dropEmptyValueLines(rep.Replace(tpl))
	}

	// The link is given the room it needs and the synopsis yields, largest
	// first, until the caption fits. How much room that is depends on the shape
	// the operator chose: the info hash alone is 60 characters and costs the
	// caption nothing, while the site's whole link measures 400 to 1200 and can
	// push the synopsis out entirely.
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
			room := captionLimit - visibleLen(rest)
			switch {
			case visibleLen(torrentShown) <= room:
				out = strings.ReplaceAll(caption, torrentTextSentinel, torrentHTML)
			case visibleLen(torrentText) <= room:
				// The line break is cosmetic. Drop it before cutting the hash:
				// a caption that can hold the URI still has to hold the URI.
				out = strings.ReplaceAll(caption, torrentTextSentinel, wrapTorrent(torrentText, magnet))
			default:
				continue
			}
			break
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
			trimmed := trimMagnet(torrentText, room)
			out = strings.ReplaceAll(caption, torrentTextSentinel, wrapTorrent(trimmed, magnet))
		}
	}
	return clampVisible(out, captionLimit)
}

// wrapTorrent renders a possibly trimmed magnet the way the caption prints it:
// inside <code> when it is the magnet that was resolved, and bare when it is the
// detail-page fallback, whose link Telegram has to keep working.
func wrapTorrent(text, magnet string) string {
	if magnet == "" {
		return escape(text)
	}
	return "<code>" + escape(text) + "</code>"
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
		if isEmptyLabelLine(line) || isEmptyLinkLine(line) || isEmptyBulletLine(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isEmptyBulletLine reports a link line whose only content is the magnet
// bullet. The shipped preset writes "🔴 {torrent_text}", and with no link that
// collapses to the bullet alone, which must not be published.
func isEmptyBulletLine(line string) bool {
	return strings.TrimSpace(line) == "🔴"
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
