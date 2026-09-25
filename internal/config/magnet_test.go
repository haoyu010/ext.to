package config

import (
	"net/url"
	"strings"
	"testing"
)

// The site writes the release name into dn verbatim, and a release name is full
// of characters a URI cannot hold. CleanMagnet is what makes the link legal
// again, and the check that it worked is a real parser rather than a string
// comparison: a URI with a raw space parses "successfully" and then hands back a
// dn that stops at the space, which is exactly how the published link failed.
func TestCleanMagnetMakesTheLinkParse(t *testing.T) {
	const hash = "urn:btih:3808e205278d073fa94e744f23a4cbc5f49ca9f8"
	const name = "The Taking of Tiger Mountain 2014 m720p BluRay x264-GeneMige [ UIndex.org ]"
	cases := []struct {
		name, in string
	}{
		{"a clean link is untouched",
			"magnet:?xt=" + hash + "&tr=udp://tracker.bittor.pw:1337/announce"},
		{"spaces and brackets in dn",
			"magnet:?xt=" + hash + "&dn=" + name + "&tr=udp://tracker.bittor.pw:1337/announce"},
		{"a dn already encoded is not encoded twice",
			"magnet:?xt=" + hash +
				"&dn=The%20Taking%20of%20Tiger%20Mountain%20%5B%20UIndex.org%20%5D" +
				"&tr=udp://tracker.bittor.pw:1337/announce"},
	}
	for _, c := range cases {
		got := CleanMagnet(c.in)
		if strings.ContainsAny(got, " <>[]") {
			t.Errorf("%s: the result still holds an unsafe character: %q", c.name, got)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Errorf("%s: the result does not parse: %v", c.name, err)
			continue
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			t.Errorf("%s: its query does not parse: %v", c.name, err)
			continue
		}
		if q.Get("xt") != hash {
			t.Errorf("%s: the info hash changed: %q", c.name, q.Get("xt"))
		}
		if want := "udp://tracker.bittor.pw:1337/announce"; q.Get("tr") != want {
			t.Errorf("%s: tr = %q, want %q", c.name, q.Get("tr"), want)
		}
	}

	// The value is what a reader ends up with, so it has to survive intact: a
	// strict parser must see the whole release name, not the first word of it.
	got := CleanMagnet("magnet:?xt=" + hash + "&dn=" + name)
	q, err := url.ParseQuery(strings.TrimPrefix(got, "magnet:?"))
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if dn := q.Get("dn"); dn != name {
		t.Errorf("dn decoded to\n  %q\nwant\n  %q", dn, name)
	}

	// A clean link is returned byte for byte, so an install whose magnets are
	// already well formed is not rewritten.
	clean := "magnet:?xt=" + hash + "&tr=udp://tracker.bittor.pw:1337/announce"
	if CleanMagnet(clean) != clean {
		t.Errorf("a clean link was rewritten:\n  %q", CleanMagnet(clean))
	}
}

// A fragment with no "=" is the shape the site's own escaping bug produces when
// a release name carries a raw "&": the name is split and the tail arrives as a
// parameter of its own. It cannot be reattached reliably, and keeping it would
// publish a URI whose parameter names are release-name prose, so it is dropped.
func TestCleanMagnetDropsFragmentsThatAreNotParameters(t *testing.T) {
	// Measured on the deployed install: this release name carries a raw "&",
	// and the site emitted " AAC 2.0) - 2.1GB - ESub.mkv" as a parameter.
	in := "magnet:?xt=urn:btih:ab305ad9d89b0eefbe606d6f4637629f796308d4" +
		"&dn=www.1TamilMV.lease - Mango Pachcha (2026) Telugu HQ HDRip" +
		"& AAC 2.0) - 2.1GB - ESub.mkv" +
		"&tr=udp://tracker.bittor.pw:1337/announce"
	got := CleanMagnet(in)
	if strings.Contains(got, "2.1GB") {
		t.Errorf("a fragment with no parameter name was kept: %q", got)
	}
	q, err := url.ParseQuery(strings.TrimPrefix(got, "magnet:?"))
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if q.Get("xt") == "" || q.Get("tr") == "" {
		t.Errorf("a real parameter was dropped with the junk: %q", got)
	}
	// Every parameter that is left has to have a name.
	for _, p := range strings.Split(strings.TrimPrefix(got, "magnet:?"), "&") {
		if name, _, ok := strings.Cut(p, "="); !ok || name == "" {
			t.Errorf("parameter %q has no name", p)
		}
	}

	// A link whose every parameter is junk is returned as it came in: mangling
	// it further would only make the failure harder to see.
	junk := "magnet:? just some words"
	if CleanMagnet(junk) != junk {
		t.Errorf("CleanMagnet(%q) = %q, want it unchanged", junk, CleanMagnet(junk))
	}
	// A fragment that has an "=" but nothing before it is junk too: "=value" is
	// not a parameter, and keeping it would publish an empty name.
	orphan := "magnet:?xt=urn:btih:AB&=orphaned value&tr=udp://t.example:6969"
	got = CleanMagnet(orphan)
	if strings.Contains(got, "orphaned") {
		t.Errorf("a value with no parameter name was kept: %q", got)
	}
	if _, err := url.ParseQuery(strings.TrimPrefix(got, "magnet:?")); err != nil {
		t.Errorf("the repaired link does not parse: %v", err)
	}
	// Anything that is not a magnet is not this function's business.
	for _, other := range []string{"https://ext.to/x-1/", "", "magnetx:?a=b"} {
		if got := CleanMagnet(other); got != other {
			t.Errorf("CleanMagnet(%q) = %q, want it unchanged", other, got)
		}
	}
}

// The button that copies the magnet has a 256-character ceiling, measured
// against the live Bot API, and a magnet carrying trackers is often longer. What
// it must never do is cut the link mid-URL: the reader would get a string that
// looks like a link and is not one.
func TestCopyMagnetFitsTheButtonAndStaysALink(t *testing.T) {
	hash := "magnet:?xt=urn:btih:3808e205278d073fa94e744f23a4cbc5f49ca9f8"
	trackers := []string{
		"udp://tracker.bittor.pw:1337/announce",
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://tracker.dler.org:6969/announce",
		"udp://open.stealth.si:80/announce",
		"udp://tracker.torrent.eu.org:451/announce",
		"udp://exodus.desync.com:6969/announce",
		"udp://open.demonii.com:1337/announce",
	}
	long := hash + "&dn=Some Release [ Group ]" +
		strings.Join(prefixEach(trackers, "&tr="), "")

	got := CopyMagnet(long)
	if n := len([]rune(got)); n > copyTextLimit {
		t.Errorf("the button text is %d characters, over the %d limit: %q",
			n, copyTextLimit, got)
	}
	if !strings.HasPrefix(got, hash) {
		t.Errorf("the info hash was lost: %q", got)
	}
	if strings.ContainsAny(got, " <>[]") {
		t.Errorf("the button text is not a legal URI: %q", got)
	}
	// It ends on a parameter boundary, so it stays a link a client can act on.
	for _, p := range strings.Split(strings.TrimPrefix(got, "magnet:?"), "&") {
		if !strings.HasPrefix(p, "xt=urn:btih:") && !strings.HasPrefix(p, "tr=") &&
			!strings.HasPrefix(p, "dn=") {
			t.Errorf("the button text holds a half parameter %q in %q", p, got)
		}
	}
	if _, err := url.Parse(got); err != nil {
		t.Errorf("the button text does not parse: %v", err)
	}
	// The limit is a ceiling, not a target: a short link is passed through whole
	// and keeps its display name.
	short := hash + "&dn=Short.Name.mkv"
	if CopyMagnet(short) != short {
		t.Errorf("a short magnet was trimmed: %q", CopyMagnet(short))
	}
}

func prefixEach(items []string, prefix string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = prefix + s
	}
	return out
}
