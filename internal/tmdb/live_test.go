package tmdb

import (
	"context"
	"os"
	"testing"
)

// liveKeyEnv holds a real TMDB credential. The classification fields are only
// exercised against fixtures in the other tests, and a fixture cannot prove the
// JSON field names match what TMDB actually sends, so this checks the real API
// when a key is supplied.
const liveKeyEnv = "TMDB_API_KEY"

func TestLiveMetadataFields(t *testing.T) {
	key := os.Getenv(liveKeyEnv)
	if key == "" {
		t.Skipf("set %s to exercise the real TMDB API", liveKeyEnv)
	}

	c := New(key, "zh-CN")
	cases := []struct {
		imdbID  string
		release string
		kind    string
	}{
		{"tt0137523", "[电影] Fight Club (1999) 1080p BluRay", "movie"},
		{"tt0903747", "[剧集] Breaking Bad (2008) S01 1080p", "tv"},
	}
	for _, tc := range cases {
		// The release name supplies the kind, which is what the real caller
		// always has; passing an empty name would exercise the unknown-kind
		// path instead and is not what this test is about.
		entry, err := c.Resolve(context.Background(), tc.imdbID, tc.release, "")
		if err != nil {
			t.Fatalf("%s: Resolve: %v", tc.imdbID, err)
		}
		if entry.Type != tc.kind {
			t.Errorf("%s: type = %q, want %q", tc.imdbID, entry.Type, tc.kind)
		}
		if len(entry.Genres) == 0 {
			t.Errorf("%s: no genres were decoded; the JSON field name is wrong", tc.imdbID)
		}
		if entry.OriginalLanguage == "" {
			t.Errorf("%s: original_language was not decoded", tc.imdbID)
		}
		if tc.kind == "tv" && len(entry.OriginCountries) == 0 {
			t.Errorf("%s: origin_country was not decoded", tc.imdbID)
		}
		if tc.kind == "movie" && len(entry.ProductionCountries) == 0 {
			t.Errorf("%s: production_countries was not decoded", tc.imdbID)
		}
	}
}
