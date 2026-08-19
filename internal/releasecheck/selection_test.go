package releasecheck

import (
	"testing"
	"time"
)

func TestSelectUpdateStableCurrentOnlyAcceptsNewerStableV2(t *testing.T) {
	releases := []Release{
		testRelease("v2.0.1-beta.1", "2026-08-18T00:00:00Z"),
		testRelease("v2.0.1", "2026-08-19T00:00:00Z"),
		testRelease("v2.2.0-rc.1", "2026-08-20T00:00:00Z"),
		testRelease("v2.1.0", "2026-08-21T00:00:00Z"),
		testRelease("v3.0.0", "2026-08-22T00:00:00Z"),
	}

	got := SelectUpdate("v2.0.0", releases)
	assertUpdate(t, got, "v2.1.0", "2026-08-21T00:00:00Z")
}

func TestSelectUpdateTestCurrentAcceptsAnyNewerV2Release(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		releases []Release
		want     string
	}{
		{
			name:    "beta selects later beta over stable patch line",
			current: "v2.0.0-beta.7",
			releases: []Release{
				testRelease("v2.0.0-beta.8", "2026-08-18T00:00:00Z"),
				testRelease("v2.0.0", "2026-08-19T00:00:00Z"),
				testRelease("v2.1.0-beta.1", "2026-08-20T00:00:00Z"),
			},
			want: "v2.1.0-beta.1",
		},
		{
			name:    "rc selects stable",
			current: "2.0.0-rc.1",
			releases: []Release{
				testRelease("v2.0.0-rc.2", "2026-08-18T00:00:00Z"),
				testRelease("v2.0.0", "2026-08-19T00:00:00Z"),
			},
			want: "v2.0.0",
		},
		{
			name:    "dev participates as an ordinary test version",
			current: "2.0.0-dev",
			releases: []Release{
				testRelease("v2.0.0-rc.1", "2026-08-18T00:00:00Z"),
				testRelease("v2.0.0", "2026-08-19T00:00:00Z"),
			},
			want: "v2.0.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SelectUpdate(test.current, test.releases)
			if got == nil || got.Version != test.want {
				t.Fatalf("SelectUpdate(%q) = %#v, want %q", test.current, got, test.want)
			}
		})
	}
}

func TestSelectUpdateUsesSemVerPrereleasePrecedence(t *testing.T) {
	releases := []Release{
		testRelease("v2.0.0-beta.11", "2026-08-18T00:00:00Z"),
		testRelease("v2.0.0-rc.2", "2026-08-19T00:00:00Z"),
		testRelease("v2.0.0-rc.10", "2026-08-20T00:00:00Z"),
	}

	got := SelectUpdate("v2.0.0-beta.9", releases)
	assertUpdate(t, got, "v2.0.0-rc.10", "2026-08-20T00:00:00Z")
}

func TestSelectUpdateRejectsIneligibleReleases(t *testing.T) {
	releases := []Release{
		testRelease("v2.0.0-beta.7", "2026-08-18T00:00:00Z"),
		testRelease("v2.0.0-beta.6", "2026-08-17T00:00:00Z"),
		testRelease("v3.0.0", "2026-08-20T00:00:00Z"),
		testRelease("v2.0.0+build.1", "2026-08-21T00:00:00Z"),
		testRelease("v2.0.0-rc.01", "2026-08-22T00:00:00Z"),
		{TagName: "v2.0.0-rc.1", HTMLURL: testReleaseURL("v2.0.0-rc.1"), Draft: true},
	}

	if got := SelectUpdate("v2.0.0-beta.7", releases); got != nil {
		t.Fatalf("SelectUpdate() = %#v, want nil", got)
	}
	for _, current := range []string{"", "latest", "v1.4.9", "v3.0.0-beta.1"} {
		if got := SelectUpdate(current, []Release{testRelease("v2.1.0", "2026-08-23T00:00:00Z")}); got != nil {
			t.Fatalf("SelectUpdate(%q) = %#v, want nil", current, got)
		}
	}
}

func TestSelectUpdateRejectsUntrustedOrIncompleteMetadata(t *testing.T) {
	releases := []Release{
		{TagName: "v2.0.1", HTMLURL: "http://github.com/tbphp/gpt-load/releases/tag/v2.0.1", PublishedAt: time.Now()},
		{TagName: "v2.0.2", HTMLURL: "https://evil.test/tbphp/gpt-load/releases/tag/v2.0.2", PublishedAt: time.Now()},
		{TagName: "v2.0.3", HTMLURL: testReleaseURL("v2.0.2"), PublishedAt: time.Now()},
		{TagName: "v2.0.4", HTMLURL: testReleaseURL("v2.0.4")},
	}

	if got := SelectUpdate("v2.0.0", releases); got != nil {
		t.Fatalf("SelectUpdate() = %#v, want nil", got)
	}
}

func testRelease(tag, published string) Release {
	return Release{
		TagName:     tag,
		HTMLURL:     testReleaseURL(tag),
		PublishedAt: mustParseReleaseTime(published),
	}
}

func testReleaseURL(tag string) string {
	return "https://github.com/tbphp/gpt-load/releases/tag/" + tag
}

func mustParseReleaseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func assertUpdate(t *testing.T, got *Update, version, published string) {
	t.Helper()
	if got == nil || got.Version != version || got.ReleaseURL != testReleaseURL(version) ||
		got.PublishedAtMS != mustParseReleaseTime(published).UnixMilli() {
		t.Fatalf("SelectUpdate() = %#v, want version=%q published=%q", got, version, published)
	}
}
