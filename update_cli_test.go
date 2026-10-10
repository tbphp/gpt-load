package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/releasecheck"
)

func TestSelfUpdaterReplacesExecutableWithVerifiedRelease(t *testing.T) {
	executable := writeTestExecutable(t, "old binary")
	newBinary := []byte("new binary")
	server := newReleaseAssetServer(t, map[string][]byte{
		"/download/v2.0.0-rc.2/SHA256SUMS": []byte(
			sha256SumsLine([]byte("other"), "gpt-load-linux-arm64") +
				sha256SumsLine(newBinary, "gpt-load-linux-amd64"),
		),
		"/download/v2.0.0-rc.2/gpt-load-linux-amd64": newBinary,
	})
	fetcher := &fakeReleaseFetcher{releases: []releasecheck.Release{
		testUpdateRelease("v2.0.0-rc.1"),
		testUpdateRelease("v2.0.0-rc.2"),
	}}
	var stdout bytes.Buffer
	updater := newTestSelfUpdater("v2.0.0-rc.1", fetcher, server, executable, &stdout)

	if err := updater.run(t.Context()); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(newBinary) {
		t.Fatalf("executable content = %q, want %q", content, newBinary)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(executable)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o750 {
			t.Fatalf("executable mode = %v, want 0750", info.Mode().Perm())
		}
	}
	assertOnlyExecutableRemains(t, executable)
	wantPaths := []string{
		"/download/v2.0.0-rc.2/SHA256SUMS",
		"/download/v2.0.0-rc.2/gpt-load-linux-amd64",
	}
	if got := server.requestedPaths(); !slices.Equal(got, wantPaths) {
		t.Fatalf("requested paths = %v, want %v", got, wantPaths)
	}
	for _, want := range []string{
		"Updated GPT-Load to v2.0.0-rc.2.",
		"https://github.com/tbphp/gpt-load/releases/tag/v2.0.0-rc.2",
		"Restart GPT-Load to apply the update.",
		"Back up your data before restarting.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout does not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestSelfUpdaterReportsUpToDateWithoutDownloading(t *testing.T) {
	executable := writeTestExecutable(t, "current binary")
	server := newReleaseAssetServer(t, nil)
	fetcher := &fakeReleaseFetcher{releases: []releasecheck.Release{
		testUpdateRelease("v1.4.12"),
		testUpdateRelease("v2.0.0-rc.1"),
		testUpdateRelease("v3.0.0"),
	}}
	var stdout bytes.Buffer
	updater := newTestSelfUpdater("v2.0.0-rc.1", fetcher, server, executable, &stdout)

	if err := updater.run(t.Context()); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if stdout.String() != "GPT-Load v2.0.0-rc.1 is up to date.\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if paths := server.requestedPaths(); len(paths) != 0 {
		t.Fatalf("requested paths = %v, want none", paths)
	}
	assertExecutableContent(t, executable, "current binary")
}

func TestSelfUpdaterKeepsExecutableWhenVerificationFails(t *testing.T) {
	tests := []struct {
		name      string
		sums      string
		wantError string
		wantPaths []string
	}{
		{
			name:      "checksum mismatch",
			sums:      sha256SumsLine([]byte("expected binary"), "gpt-load-linux-amd64"),
			wantError: "checksum mismatch",
			wantPaths: []string{
				"/download/v2.0.0-rc.2/SHA256SUMS",
				"/download/v2.0.0-rc.2/gpt-load-linux-amd64",
			},
		},
		{
			name:      "missing checksum entry",
			sums:      sha256SumsLine([]byte("tampered binary"), "gpt-load-linux-arm64"),
			wantError: "SHA256SUMS",
			wantPaths: []string{"/download/v2.0.0-rc.2/SHA256SUMS"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executable := writeTestExecutable(t, "old binary")
			server := newReleaseAssetServer(t, map[string][]byte{
				"/download/v2.0.0-rc.2/SHA256SUMS":           []byte(test.sums),
				"/download/v2.0.0-rc.2/gpt-load-linux-amd64": []byte("tampered binary"),
			})
			fetcher := &fakeReleaseFetcher{releases: []releasecheck.Release{testUpdateRelease("v2.0.0-rc.2")}}
			var stdout bytes.Buffer
			updater := newTestSelfUpdater("v2.0.0-rc.1", fetcher, server, executable, &stdout)

			err := updater.run(t.Context())

			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("run() error = %v, want %q", err, test.wantError)
			}
			assertExecutableContent(t, executable, "old binary")
			assertOnlyExecutableRemains(t, executable)
			if got := server.requestedPaths(); !slices.Equal(got, test.wantPaths) {
				t.Fatalf("requested paths = %v, want %v", got, test.wantPaths)
			}
		})
	}
}

func TestSelfUpdaterRejectsBuildsWithoutOfficialReleases(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		goos      string
		wantError string
	}{
		{name: "development build", current: "2.0.0-dev", goos: "linux", wantError: "official release"},
		{name: "unsupported platform", current: "v2.0.0-rc.1", goos: "freebsd", wantError: "freebsd/amd64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executable := writeTestExecutable(t, "current binary")
			fetcher := &fakeReleaseFetcher{releases: []releasecheck.Release{testUpdateRelease("v2.0.0")}}
			var stdout bytes.Buffer
			updater := newTestSelfUpdater(test.current, fetcher, nil, executable, &stdout)
			updater.goos = test.goos

			err := updater.run(t.Context())

			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("run() error = %v, want %q", err, test.wantError)
			}
			if fetcher.calls != 0 {
				t.Fatalf("fetch calls = %d, want 0", fetcher.calls)
			}
			assertExecutableContent(t, executable, "current binary")
		})
	}
}

func TestSelfUpdaterReportsReleaseFetchFailure(t *testing.T) {
	executable := writeTestExecutable(t, "current binary")
	fetcher := &fakeReleaseFetcher{err: errors.New("fetch GitHub releases: status 403")}
	var stdout bytes.Buffer
	updater := newTestSelfUpdater("v2.0.0-rc.1", fetcher, nil, executable, &stdout)

	err := updater.run(t.Context())

	if err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("run() error = %v, want fetch failure", err)
	}
	assertExecutableContent(t, executable, "current binary")
}

func TestDownloadReleaseAssetRejectsUnusableResponses(t *testing.T) {
	server := newReleaseAssetServer(t, map[string][]byte{
		"/download/v2.0.0/SHA256SUMS": bytes.Repeat([]byte("x"), 33),
	})
	updater := selfUpdater{
		httpClient:   server.server.Client(),
		downloadBase: server.server.URL + "/download",
	}
	for _, asset := range []string{"SHA256SUMS", "missing"} {
		var output bytes.Buffer
		if err := updater.download(t.Context(), "v2.0.0", asset, 32, &output); err == nil {
			t.Fatalf("download(%s) error = nil, want error", asset)
		}
	}
}

func TestCheckReleaseDownloadRedirectAllowsOnlyGitHubHTTPS(t *testing.T) {
	via := []*http.Request{
		mustNewRequest(t, "https://github.com/tbphp/gpt-load/releases/download/v2.0.0/SHA256SUMS"),
	}
	tests := []struct {
		target  string
		allowed bool
	}{
		{target: "https://release-assets.githubusercontent.com/github-production-release-asset/1", allowed: true},
		{target: "https://objects.githubusercontent.com/github-production-release-asset-2e65be/1", allowed: true},
		{target: "https://github.com/tbphp/gpt-load/releases/download/v2.0.0/SHA256SUMS", allowed: true},
		{target: "http://release-assets.githubusercontent.com/asset", allowed: false},
		{target: "https://example.com/asset", allowed: false},
		{target: "https://githubusercontent.com.example.com/asset", allowed: false},
		{target: "https://evilgithubusercontent.com/asset", allowed: false},
	}
	for _, test := range tests {
		err := checkReleaseDownloadRedirect(mustNewRequest(t, test.target), via)
		if (err == nil) != test.allowed {
			t.Fatalf("redirect to %s error = %v, allowed = %t", test.target, err, test.allowed)
		}
	}

	tooMany := make([]*http.Request, 10)
	for index := range tooMany {
		tooMany[index] = via[0]
	}
	if err := checkReleaseDownloadRedirect(mustNewRequest(t, tests[0].target), tooMany); err == nil {
		t.Fatal("redirect after 10 hops error = nil, want error")
	}
}

func TestExpectedSHA256ReadsSha256sumOutput(t *testing.T) {
	hash := strings.Repeat("a", 64)
	sums := strings.Repeat("b", 64) + "  gpt-load-linux-arm64\n" +
		strings.ToUpper(hash) + " *gpt-load-linux-amd64\r\n"

	got, err := expectedSHA256([]byte(sums), "gpt-load-linux-amd64")
	if err != nil || got != hash {
		t.Fatalf("expectedSHA256() = %q, %v, want %q", got, err, hash)
	}

	for _, invalid := range []string{
		hash + "  gpt-load-linux-arm64\n",
		"not-a-hash  gpt-load-linux-amd64\n",
		strings.Repeat("g", 64) + "  gpt-load-linux-amd64\n",
	} {
		if got, err := expectedSHA256([]byte(invalid), "gpt-load-linux-amd64"); err == nil {
			t.Fatalf("expectedSHA256(%q) = %q, want error", invalid, got)
		}
	}
}

func TestReleaseBinaryNameMatchesPublishedAssets(t *testing.T) {
	inventory, err := os.ReadFile(filepath.Join(".github", "release-assets.txt"))
	if err != nil {
		t.Fatal(err)
	}
	published := strings.Fields(string(inventory))
	if !slices.Contains(published, releaseChecksumAsset) {
		t.Fatalf("release assets do not include %s", releaseChecksumAsset)
	}
	for platform, want := range map[string]string{
		"linux/amd64":   "gpt-load-linux-amd64",
		"linux/arm64":   "gpt-load-linux-arm64",
		"darwin/amd64":  "gpt-load-macos-amd64",
		"darwin/arm64":  "gpt-load-macos-arm64",
		"windows/amd64": "gpt-load-windows-amd64.exe",
	} {
		goos, goarch, _ := strings.Cut(platform, "/")
		got, err := releaseBinaryName(goos, goarch)
		if err != nil || got != want {
			t.Fatalf("releaseBinaryName(%s) = %q, %v, want %q", platform, got, err, want)
		}
		if !slices.Contains(published, got) {
			t.Fatalf("release assets do not include %s", got)
		}
	}
	for _, platform := range []string{"windows/arm64", "linux/386", "freebsd/amd64"} {
		goos, goarch, _ := strings.Cut(platform, "/")
		if got, err := releaseBinaryName(goos, goarch); err == nil {
			t.Fatalf("releaseBinaryName(%s) = %q, want error", platform, got)
		}
	}
}

type fakeReleaseFetcher struct {
	releases []releasecheck.Release
	err      error
	calls    int
}

func (fetcher *fakeReleaseFetcher) Fetch(context.Context) ([]releasecheck.Release, error) {
	fetcher.calls++
	return fetcher.releases, fetcher.err
}

type releaseAssetServer struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []string
}

func newReleaseAssetServer(t *testing.T, assets map[string][]byte) *releaseAssetServer {
	t.Helper()
	assetServer := &releaseAssetServer{}
	assetServer.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assetServer.mu.Lock()
		assetServer.requests = append(assetServer.requests, request.URL.Path)
		assetServer.mu.Unlock()
		content, ok := assets[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		_, _ = writer.Write(content)
	}))
	t.Cleanup(assetServer.server.Close)
	return assetServer
}

func (assetServer *releaseAssetServer) requestedPaths() []string {
	assetServer.mu.Lock()
	defer assetServer.mu.Unlock()
	return slices.Clone(assetServer.requests)
}

func newTestSelfUpdater(
	current string,
	fetcher *fakeReleaseFetcher,
	server *releaseAssetServer,
	executable string,
	stdout *bytes.Buffer,
) selfUpdater {
	updater := selfUpdater{
		current:    current,
		goos:       "linux",
		goarch:     "amd64",
		fetcher:    fetcher,
		executable: func() (string, error) { return executable, nil },
		stdout:     stdout,
	}
	if server != nil {
		updater.httpClient = server.server.Client()
		updater.downloadBase = server.server.URL + "/download"
	}
	return updater
}

func testUpdateRelease(tag string) releasecheck.Release {
	return releasecheck.Release{
		TagName:     tag,
		HTMLURL:     "https://github.com/tbphp/gpt-load/releases/tag/" + tag,
		PublishedAt: time.Date(2026, 10, 6, 9, 47, 44, 0, time.UTC),
	}
}

func sha256SumsLine(content []byte, name string) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

func writeTestExecutable(t *testing.T, content string) string {
	t.Helper()
	name := "gpt-load"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o750); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertExecutableContent(t *testing.T, executable, want string) {
	t.Helper()
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("executable content = %q, want %q", content, want)
	}
}

func assertOnlyExecutableRemains(t *testing.T, executable string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(executable))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{filepath.Base(executable)}) {
		t.Fatalf("directory entries = %v, want only %s", names, filepath.Base(executable))
	}
}

func mustNewRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
