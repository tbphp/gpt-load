package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/releasecheck"
)

const (
	releaseDownloadBaseURL  = "https://github.com/tbphp/gpt-load/releases/download"
	releaseChecksumAsset    = "SHA256SUMS"
	maxReleaseChecksumBytes = int64(64 << 10)
	maxReleaseBinaryBytes   = int64(512 << 20)
)

type releaseFetcher interface {
	Fetch(context.Context) ([]releasecheck.Release, error)
}

// selfUpdater 把当前可执行文件替换为同一主版本内最新的官方发布构建。
// 选版规则与管理界面的更新提示相同；它只替换文件，不停止、启动或重启任何进程。
type selfUpdater struct {
	current      string
	goos         string
	goarch       string
	fetcher      releaseFetcher
	httpClient   *http.Client
	downloadBase string
	executable   func() (string, error)
	stdout       io.Writer
}

func runUpdateCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "update does not accept arguments")
		return 1
	}
	// 中断时取消下载，让临时文件走正常的失败清理。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager := httpclient.NewHTTPClientManager()
	updater := selfUpdater{
		current:      version.Version,
		goos:         runtime.GOOS,
		goarch:       runtime.GOARCH,
		fetcher:      releasecheck.NewClient(manager, nil),
		httpClient:   newReleaseDownloadClient(manager),
		downloadBase: releaseDownloadBaseURL,
		executable:   currentExecutable,
		stdout:       stdout,
	}
	if err := updater.run(ctx); err != nil {
		fmt.Fprintf(stderr, "Update failed: %v\n", err)
		return 1
	}
	return 0
}

func (updater selfUpdater) run(ctx context.Context) error {
	if !releasecheck.IsReleaseVersion(updater.current) {
		return fmt.Errorf("only official release builds can be updated; this build is %s", updater.current)
	}
	asset, err := releaseBinaryName(updater.goos, updater.goarch)
	if err != nil {
		return err
	}
	releases, err := updater.fetcher.Fetch(ctx)
	if err != nil {
		return err
	}
	update := releasecheck.SelectUpdate(updater.current, releases)
	if update == nil {
		fmt.Fprintf(updater.stdout, "GPT-Load %s is up to date.\n", updater.current)
		return nil
	}
	target, err := updater.executable()
	if err != nil {
		return err
	}

	fmt.Fprintf(updater.stdout, "Updating GPT-Load %s to %s...\n", updater.current, update.Version)
	if err := updater.install(ctx, update.Version, asset, target); err != nil {
		return err
	}
	fmt.Fprintf(updater.stdout, "Updated GPT-Load to %s.\n", update.Version)
	fmt.Fprintf(updater.stdout, "Release notes: %s\n", update.ReleaseURL)
	fmt.Fprintln(updater.stdout, "Restart GPT-Load to apply the update.")
	fmt.Fprintln(updater.stdout, "The new version may migrate the database when it starts, and older versions cannot open a migrated database. Back up your data before restarting.")
	return nil
}

func (updater selfUpdater) install(ctx context.Context, tag, asset, target string) error {
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", target, err)
	}
	// 临时文件放在同一目录，最后的 rename 才是同一文件系统内的原子替换；
	// 先创建它也能在下载前发现没有写权限。
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".update-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf(
				"cannot write to %s (run the update as a user who can modify %s): %w",
				filepath.Dir(target),
				target,
				fs.ErrPermission,
			)
		}
		return fmt.Errorf("create temporary file next to %s: %w", target, err)
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		if !installed {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()

	var sums bytes.Buffer
	if err := updater.download(ctx, tag, releaseChecksumAsset, maxReleaseChecksumBytes, &sums); err != nil {
		return err
	}
	expected, err := expectedSHA256(sums.Bytes(), asset)
	if err != nil {
		return err
	}

	fmt.Fprintf(updater.stdout, "Downloading %s...\n", asset)
	digest := sha256.New()
	if err := updater.download(ctx, tag, asset, maxReleaseBinaryBytes, io.MultiWriter(temporary, digest)); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("set permissions on %s: %w", temporaryPath, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", temporaryPath, err)
	}
	if err := replaceExecutable(target, temporaryPath); err != nil {
		return err
	}
	installed = true
	return nil
}

func (updater selfUpdater) download(
	ctx context.Context,
	tag string,
	asset string,
	limit int64,
	destination io.Writer,
) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		updater.downloadBase+"/"+tag+"/"+asset,
		nil,
	)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	request.Header.Set("User-Agent", "GPT-Load")
	response, err := updater.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", asset, response.StatusCode)
	}
	written, err := io.Copy(destination, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	if written > limit {
		return fmt.Errorf("download %s: response exceeds %d bytes", asset, limit)
	}
	return nil
}

func newReleaseDownloadClient(manager *httpclient.HTTPClientManager) *http.Client {
	client := *manager.GetClient(&httpclient.Config{
		ConnectTimeout:        10 * time.Second,
		RequestTimeout:        30 * time.Minute,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   2,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	})
	// Release 资产会从 github.com 跳转到 GitHub 的存储域名，默认的同源跳转策略会拒绝。
	client.CheckRedirect = checkReleaseDownloadRedirect
	return &client
}

// checkReleaseDownloadRedirect 只允许跳转到 GitHub 的 HTTPS 域名；文件内容另由 SHA256SUMS 校验。
func checkReleaseDownloadRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	host := strings.ToLower(request.URL.Hostname())
	if request.URL.Scheme != "https" ||
		(host != "github.com" && !strings.HasSuffix(host, ".githubusercontent.com")) {
		return fmt.Errorf("redirect to %s://%s is not allowed", request.URL.Scheme, host)
	}
	return nil
}

// expectedSHA256 从 sha256sum 格式的 SHA256SUMS 中读取 name 对应的摘要。
func expectedSHA256(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		digest := strings.ToLower(fields[0])
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("%s has an invalid entry for %s", releaseChecksumAsset, name)
		}
		return digest, nil
	}
	return "", fmt.Errorf("%s does not list %s", releaseChecksumAsset, name)
}

// releaseBinaryName 返回当前平台在 GitHub Release 中的文件名，需与 release.yml 的构建矩阵保持一致。
func releaseBinaryName(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64":
		return "gpt-load-linux-" + goarch, nil
	case "darwin/amd64", "darwin/arm64":
		return "gpt-load-macos-" + goarch, nil
	case "windows/amd64":
		return "gpt-load-windows-amd64.exe", nil
	default:
		return "", fmt.Errorf("no official release build is available for %s/%s", goos, goarch)
	}
}

func currentExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	// 通过符号链接启动时替换真实文件，链接本身保持不变。
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	return resolved, nil
}
