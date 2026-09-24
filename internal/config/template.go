package config

import (
	"fmt"
	"html"
	"strings"
)

// DefaultTemplate is the caption used for new installs. It is rendered as
// Telegram HTML, so only <b>, <i>, <a>, <code> and <pre> tags are allowed.
const DefaultTemplate = `<b>{title}</b>

📁 {category}
💾 {size} · 📄 {files} files
🌱 {seeds} seeders · {leeches} leechers
🕐 {age}

<a href="{url}">Open on ext.to</a>`

// TMDBTemplate is an optional caption preset that leads with the matched
// TMDB entry. It is offered in the dashboard rather than applied by default,
// because it only renders well once a TMDB key is configured.
const TMDBTemplate = `<b>{tmdb_title}</b> ({tmdb_year})
⭐ {tmdb_rating}/10 · {tmdb_votes} votes

📁 {category_tmdb}
💾 {size} · 📄 {files} files
🌱 {seeds} seeders · {leeches} leechers
🕐 {age}

<a href="{tmdb_url}">TMDB</a> · <a href="{url}">ext.to</a>`

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
	{"{tmdb_overview}", "TMDB 简介，会按 Telegram 限制截断"},
	{"{category_tmdb}", "TMDB 分类规则命中的分类，例如 国漫、国产剧，未命中时为「未分类」"},
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
	TMDBOverview      string
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
	// Like the TMDB title, the classified name degrades to the tracker's own
	// category rather than rendering an empty value, so a template using it
	// never leaves a dangling label.
	ruleCategory := d.RuleCategory
	if ruleCategory == "" {
		ruleCategory = d.Category
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
		"{tmdb_overview}", escape(truncateRunes(d.TMDBOverview, 320)),
		"{category_tmdb}", escape(ruleCategory),
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
		"{id}", fmt.Sprint(d.ID),
	)
	return rep.Replace(tpl)
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
