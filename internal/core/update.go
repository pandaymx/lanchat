package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// CurrentVersion 当前客户端核心版本。
const CurrentVersion = "1.10.2"

// DefaultReleaseBaseURL GitHub Releases 默认基础地址。
const DefaultReleaseBaseURL = "https://github.com/pandaymx/lanchat/releases"

// DefaultDomesticProxies 常用国内 GitHub 加速代理列表（按优先级自动回退，末尾空串为 GitHub 直连兜底）。
var DefaultDomesticProxies = []string{
	"https://ghfast.top/",
	"https://gh-proxy.com/",
	"https://ghproxy.net/",
	"https://mirror.ghproxy.com/",
	"https://ghps.cc/",
	"",
}

// ReleaseVersionInfo 对应 GitHub Release 发布的 version.json 清单。
type ReleaseVersionInfo struct {
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	Commit      string `json:"commit"`
	PublishedAt string `json:"published_at"`
}

// UpdateResult 描述一次版本检查或静默下载的结果。
type UpdateResult struct {
	CurrentVersion string
	LatestVersion  string
	Tag            string
	Mirror         string
	AssetName      string
	SavedPath      string
	SHA256Verified bool
	HasNewVersion  bool
	Summary        string
}

func (c *Client) effectiveOS() string {
	c.mu.Lock()
	osName := strings.ToLower(strings.TrimSpace(c.os))
	c.mu.Unlock()
	if osName == "" {
		osName = runtime.GOOS
	}
	return osName
}

func (c *Client) resolveUpdateDownloadDir() string {
	c.mu.Lock()
	dir := strings.TrimSpace(c.downloadDir)
	c.mu.Unlock()
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return dir
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dl := filepath.Join(home, "Downloads")
		if err := os.MkdirAll(dl, 0o755); err == nil {
			return dl
		}
	}
	return os.TempDir()
}

// releaseAssetCandidatesForOS 返回目标平台优先下载的 Release 制品文件名候选列表。
func releaseAssetCandidatesForOS(osName, version string) []string {
	ver := strings.TrimPrefix(strings.TrimSpace(version), "v")
	switch strings.ToLower(strings.TrimSpace(osName)) {
	case "windows", "win":
		return []string{
			fmt.Sprintf("LANChat-%s-windows-x64.msi", ver),
			fmt.Sprintf("LANChat-%s-windows-x64-portable.zip", ver),
		}
	case "linux":
		return []string{
			fmt.Sprintf("LANChat-%s-linux-amd64.deb", ver),
			fmt.Sprintf("LANChat-%s-linux-x64.tar.gz", ver),
		}
	case "macos", "darwin":
		return []string{
			fmt.Sprintf("LANChat-%s-macos-universal-unsigned.zip", ver),
		}
	case "android":
		return []string{
			fmt.Sprintf("LANChat-%s-android-release.apk", ver),
		}
	case "ios":
		return []string{
			fmt.Sprintf("LANChat-%s-ios-simulator-unsigned.zip", ver),
		}
	default:
		return []string{
			fmt.Sprintf("LANChat-%s-linux-x64.tar.gz", ver),
		}
	}
}

// BuildProxiedURL 将原始 GitHub Release 地址包装为指定国内加速代理地址。
func BuildProxiedURL(proxyPrefix, targetURL string) string {
	prefix := strings.TrimSpace(proxyPrefix)
	if prefix == "" || prefix == "direct" {
		return targetURL
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(targetURL, "/")
}

func proxyDisplayName(proxyPrefix string) string {
	prefix := strings.TrimSpace(proxyPrefix)
	if prefix == "" || prefix == "direct" {
		return "github.com(直连)"
	}
	if u, err := url.Parse(prefix); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.Trim(prefix, "/")
}

func (c *Client) effectiveUpdateProxies(customProxy string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "direct" {
			p = ""
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if strings.TrimSpace(customProxy) != "" {
		add(customProxy)
	}
	if envProxy := strings.TrimSpace(os.Getenv("LANCHAT_UPDATE_PROXY")); envProxy != "" {
		for _, part := range strings.Split(envProxy, ",") {
			if strings.TrimSpace(part) != "" {
				add(part)
			}
		}
	}
	c.mu.Lock()
	configured := append([]string(nil), c.updateProxies...)
	c.mu.Unlock()
	if len(configured) > 0 {
		for _, p := range configured {
			add(p)
		}
		return out
	}
	for _, p := range DefaultDomesticProxies {
		add(p)
	}
	return out
}

func (c *Client) effectiveUpdateBaseURL() string {
	c.mu.Lock()
	base := strings.TrimSpace(c.updateBaseURL)
	c.mu.Unlock()
	if base != "" {
		return strings.TrimRight(base, "/")
	}
	if envBase := strings.TrimSpace(os.Getenv("LANCHAT_UPDATE_BASE_URL")); envBase != "" {
		return strings.TrimRight(envBase, "/")
	}
	return DefaultReleaseBaseURL
}

func (c *Client) effectiveUpdateHTTPClient(timeout time.Duration) *http.Client {
	c.mu.Lock()
	cli := c.updateHTTPClient
	c.mu.Unlock()
	if cli != nil {
		return cli
	}
	return &http.Client{Timeout: timeout}
}

func compareSemver(a, b string) int {
	pa := parseSemverParts(a)
	pb := parseSemverParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	return 0
}

func parseSemverParts(v string) [3]int {
	clean := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexByte(clean, '-'); idx >= 0 {
		clean = clean[:idx]
	}
	parts := strings.Split(clean, ".")
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}

func parseSHA256Sums(content string) map[string]string {
	sums := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := strings.ToLower(strings.TrimSpace(fields[0]))
		name := strings.TrimPrefix(strings.TrimSpace(fields[1]), "*")
		name = filepath.Base(name)
		if len(hash) == 64 && name != "" {
			sums[name] = hash
		}
	}
	return sums
}

// CheckAndDownloadUpdate 通过国内加速代理链检测最新 Release 版本，并在 checkOnly=false 时静默下载对应平台的安装包到本地下载目录（无需调用系统浏览器）。
func (c *Client) CheckAndDownloadUpdate(ctx context.Context, checkOnly bool, customProxy string) (*UpdateResult, error) {
	baseURL := c.effectiveUpdateBaseURL()
	proxies := c.effectiveUpdateProxies(customProxy)
	metaClient := c.effectiveUpdateHTTPClient(12 * time.Second)

	versionTargetURL := baseURL + "/latest/download/version.json"
	var verInfo ReleaseVersionInfo
	var chosenProxy string
	var lastErr error
	fetchedMeta := false

	for _, proxyPrefix := range proxies {
		reqURL := BuildProxiedURL(proxyPrefix, versionTargetURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "LANChat-Updater/"+CurrentVersion)
		resp, err := metaClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || readErr != nil {
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, proxyDisplayName(proxyPrefix))
			}
			continue
		}
		if err := json.Unmarshal(body, &verInfo); err != nil || strings.TrimSpace(verInfo.Version) == "" {
			lastErr = fmt.Errorf("invalid version.json from %s", proxyDisplayName(proxyPrefix))
			continue
		}
		chosenProxy = proxyPrefix
		fetchedMeta = true
		break
	}
	if !fetchedMeta {
		if lastErr == nil {
			lastErr = errors.New("所有国内代理与直连节点均不可达")
		}
		return nil, fmt.Errorf("获取版本清单失败: %w", lastErr)
	}

	verInfo.Version = strings.TrimPrefix(strings.TrimSpace(verInfo.Version), "v")
	if strings.TrimSpace(verInfo.Tag) == "" {
		verInfo.Tag = "v" + verInfo.Version
	}
	hasNew := compareSemver(verInfo.Version, CurrentVersion) > 0
	mirrorName := proxyDisplayName(chosenProxy)
	candidates := releaseAssetCandidatesForOS(c.effectiveOS(), verInfo.Version)

	res := &UpdateResult{
		CurrentVersion: CurrentVersion,
		LatestVersion:  verInfo.Version,
		Tag:            verInfo.Tag,
		Mirror:         mirrorName,
		AssetName:      candidates[0],
		HasNewVersion:  hasNew,
	}

	if checkOnly {
		if hasNew {
			res.Summary = fmt.Sprintf("发现新版本 v%s（当前 v%s，加速节点: %s），可执行 /update 静默下载", verInfo.Version, CurrentVersion, mirrorName)
		} else {
			res.Summary = fmt.Sprintf("当前已是最新版本 v%s（加速节点: %s）", verInfo.Version, mirrorName)
		}
		return res, nil
	}

	// 拉取 SHA256SUMS（最佳努力：若存在则强校验安装包完整性）
	sumsTargetURL := fmt.Sprintf("%s/download/%s/SHA256SUMS", baseURL, verInfo.Tag)
	expectedSums := map[string]string{}
	for _, proxyPrefix := range append([]string{chosenProxy}, proxies...) {
		reqURL := BuildProxiedURL(proxyPrefix, sumsTargetURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "LANChat-Updater/"+CurrentVersion)
		resp, err := metaClient.Do(req)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && readErr == nil {
			parsed := parseSHA256Sums(string(body))
			if len(parsed) > 0 {
				expectedSums = parsed
				break
			}
		}
	}

	destDir := c.resolveUpdateDownloadDir()
	dlClient := c.effectiveUpdateHTTPClient(3 * time.Minute)
	orderedProxies := append([]string{chosenProxy}, proxies...)

	var dlErr error
	for _, assetName := range candidates {
		assetTargetURL := fmt.Sprintf("%s/download/%s/%s", baseURL, verInfo.Tag, assetName)
		destPath := filepath.Join(destDir, assetName)
		expectedHash := expectedSums[assetName]

		for _, proxyPrefix := range orderedProxies {
			reqURL := BuildProxiedURL(proxyPrefix, assetTargetURL)
			verified, err := downloadFileWithSHA256(ctx, dlClient, reqURL, destPath, expectedHash)
			if err != nil {
				dlErr = err
				continue
			}
			res.AssetName = assetName
			res.SavedPath = destPath
			res.Mirror = proxyDisplayName(proxyPrefix)
			res.SHA256Verified = verified
			shaNote := "完成"
			if verified {
				shaNote = "SHA-256 校验通过"
			}
			res.Summary = fmt.Sprintf("已通过 [%s] 静默下载 v%s 安装包 (%s) -> %s", res.Mirror, verInfo.Version, shaNote, destPath)
			return res, nil
		}
	}

	if dlErr == nil {
		dlErr = errors.New("未找到可用安装包")
	}
	return nil, fmt.Errorf("静默下载安装包失败: %w", dlErr)
}

func downloadFileWithSHA256(ctx context.Context, cli *http.Client, reqURL, destPath, expectedHexHash string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "LANChat-Updater/"+CurrentVersion)
	resp, err := cli.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	partPath := destPath + ".part"
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(f, hasher), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(partPath)
		return false, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(partPath)
		return false, closeErr
	}
	if written == 0 {
		_ = os.Remove(partPath)
		return false, errors.New("empty response body")
	}

	actualHex := hex.EncodeToString(hasher.Sum(nil))
	verified := false
	if expectedHexHash != "" {
		if !strings.EqualFold(actualHex, expectedHexHash) {
			_ = os.Remove(partPath)
			return false, fmt.Errorf("SHA-256 mismatch: got %s, want %s", actualHex, expectedHexHash)
		}
		verified = true
	}

	if err := os.Rename(partPath, destPath); err != nil {
		_ = os.Remove(partPath)
		return false, err
	}
	return verified, nil
}

func (c *Client) handleUpdateCommand(arg, _, _ string) (string, error) {
	sub := strings.TrimSpace(arg)
	if sub == "proxies" || sub == "mirrors" {
		names := make([]string, 0, len(DefaultDomesticProxies))
		for _, p := range DefaultDomesticProxies {
			names = append(names, proxyDisplayName(p))
		}
		msg := "内置国内加速代理节点: " + strings.Join(names, " -> ")
		return "update:" + msg, nil
	}

	checkOnly := false
	customProxy := ""
	if sub == "check" {
		checkOnly = true
	} else if strings.HasPrefix(sub, "http://") || strings.HasPrefix(sub, "https://") || sub == "direct" {
		customProxy = sub
	} else if strings.HasPrefix(sub, "proxy ") {
		customProxy = strings.TrimSpace(strings.TrimPrefix(sub, "proxy "))
	}

	res, err := c.CheckAndDownloadUpdate(c.runCtx, checkOnly, customProxy)
	if err != nil {
		return "", err
	}
	return "update:" + res.Summary, nil
}
