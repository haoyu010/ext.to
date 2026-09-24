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

// TemplateFields lists the placeholders available in a caption template.
var TemplateFields = []struct{ Key, Desc string }{
	{"{title}", "Torrent title"},
	{"{category}", "Category path, for example Movies - Highres Movies"},
	{"{size}", "Human readable size, for example 1.43 GB"},
	{"{files}", "Number of files in the torrent"},
	{"{seeds}", "Seeder count"},
	{"{leeches}", "Leecher count"},
	{"{age}", "Age string reported by ext.to, for example 5 minutes ago"},
	{"{source}", "Source tracker, for example DHT or UIndex"},
	{"{uploader}", "Uploader name reported by ext.to"},
	{"{url}", "Absolute link to the torrent detail page"},
	{"{magnet}", "Magnet URI, or an empty string when unavailable"},
	{"{id}", "ext.to torrent id"},
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
}

// Render substitutes placeholders in tpl. Values are HTML-escaped because
// Telegram parses captions as HTML; callers embedding markup must escape it
// themselves.
func Render(tpl string, d TemplateData) string {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultTemplate
	}
	rep := strings.NewReplacer(
		"{title}", escape(d.Title),
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

func escape(s string) string {
	// Telegram only recognises & < > as entities, so escaping everything and
	// then decoding quotes keeps the text predictable.
	return html.EscapeString(s)
}
