package update

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// newTransport builds an HTTP transport with generous dial/TLS timeouts so
// slow or unstable networks (e.g. GitHub from mainland China) can connect.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   60 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// httpClient returns a client with the given total timeout, reusing an injected
// transport (used by tests) when present.
func (c *Checker) httpClient(timeout time.Duration) *http.Client {
	if c.Client != nil && c.Client.Transport != nil {
		return &http.Client{Timeout: timeout, Transport: c.Client.Transport}
	}
	return &http.Client{Timeout: timeout, Transport: newTransport()}
}

type Checker struct {
	ManifestURL    string
	FallbackURL    string
	CurrentVersion string
	PublicKey      ed25519.PublicKey
	Client         *http.Client
}

// DefaultFallbackURL is the direct GitHub manifest URL, used when the
// configured (accelerated) manifest URL is unreachable.
const DefaultFallbackURL = "https://github.com/LzYita/RFERP/releases/latest/download/releases.json"

func NewChecker(manifestURL, currentVersion string) (*Checker, error) {
	pub, err := PublicKey()
	if err != nil {
		return nil, err
	}
	return &Checker{
		ManifestURL:    manifestURL,
		FallbackURL:    DefaultFallbackURL,
		CurrentVersion: currentVersion,
		PublicKey:      pub,
		Client:         &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// AttemptStatus 是一次清单获取的结果分类。
//
// 为什么要分类而不是"一个 error"：`Check` 会依次试配置源与内置回退源，
// 而这两类失败的含义完全不同 ——
//
//	"这个源连不上"      → 正常容错，换个源继续，不必惊动任何人
//	"这个源给的清单验签没过" → **强信号**：密钥配错、清单被改、或发版时签错了钥匙
//
// 早前的实现把后者也当成普通错误，于是"配置源被验签拒 → 回退源说没有新版"的净结果
// 是「没有更新」—— 用户界面说"已是最新"，日志里一个字都没有。发版签错密钥这类事故
// 就表现为"用户一直不升级"且无人报错（Round 8 先决项⑤）。
type AttemptStatus string

const (
	// AttemptNewer 拿到清单、验签通过、版本更高。
	AttemptNewer AttemptStatus = "newer"
	// AttemptNotNewer 拿到清单、验签通过、版本不更高（正常情况）。
	AttemptNotNewer AttemptStatus = "not-newer"
	// AttemptRejected **拿到了清单但验签没过**。强信号，必须显眼。
	AttemptRejected AttemptStatus = "rejected"
	// AttemptUnusable 验签过了但清单本身不可用（缺字段、或声明当前版本过低）。
	AttemptUnusable AttemptStatus = "unusable"
	// AttemptUnreachable 没能拿到可用清单（网络、超时、非 200、JSON 解析失败）。
	AttemptUnreachable AttemptStatus = "unreachable"
)

// Attempt 是对一个源的一次尝试。
type Attempt struct {
	URL    string
	Status AttemptStatus
	Err    error // 非 nil 时是具体原因
}

// Result 是一次 Check 的完整结果：有没有新版，以及每个源各自是什么结果。
type Result struct {
	Manifest *Manifest // 有新版时非 nil
	Attempts []Attempt
}

// RejectedError 表示"拿到了清单但验签没过"。
//
// 单独一个类型，是为了让上层能把"这个源有问题"与"这个源暂时连不上"分开 ——
// 两者都该回退，但前者必须留下痕迹。
type RejectedError struct {
	URL string
	Err error
}

func (e *RejectedError) Error() string { return e.Err.Error() }
func (e *RejectedError) Unwrap() error { return e.Err }

// UnusableError 表示清单验签通过了、但不能用（缺字段，或声明当前版本过低）。
// 与 RejectedError 一样属于"源告诉我们发布有问题"，值得显眼。
type UnusableError struct {
	URL string
	Err error
}

func (e *UnusableError) Error() string { return e.Err.Error() }
func (e *UnusableError) Unwrap() error { return e.Err }

// Notable 返回需要被看见的尝试：验签被拒、清单不可用。
//
// **不含 unreachable**：配置源连不上、回退源顶上，是设计好的容错路径，不该每次都
// 报警。把两者混在一起是上一版可观测性问题的根源。
func (r Result) Notable() []Attempt {
	var out []Attempt
	for _, a := range r.Attempts {
		if a.Status == AttemptRejected || a.Status == AttemptUnusable {
			out = append(out, a)
		}
	}
	return out
}

// Warning 把 Notable 的尝试压成一句可供日志与界面使用的话；没有异常时返回空串。
//
// 界面据此把"已是最新版本"换成"更新源清单未通过验签，已拒绝"——这正是本条的要点：
// 让"验签被拒"不再伪装成"没有更新"。
func (r Result) Warning() string {
	notes := r.Notable()
	if len(notes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(notes))
	for _, a := range notes {
		parts = append(parts, fmt.Sprintf("%s：%v", a.URL, a.Err))
	}
	return strings.Join(parts, "；")
}

// Check 依次尝试配置源与内置回退源，返回**第一个成功**的结果，并记录每个源的尝试结果。
//
// 与早前实现的差别只在"失败时返回什么"：
//   - 返回值从 (*Manifest, error) 变成 (Result, error)，Attempts 保留了每个源各自的结果；
//   - 全都失败时优先返回**强信号错误**（验签被拒 / 清单不可用），而不是最后一个网络错误。
//     上一版会把"配置源被验签拒"丢掉、只报回退源的连接失败，把排查方向带偏。
func (c *Checker) Check() (Result, error) {
	urls := []string{c.ManifestURL}
	if c.FallbackURL != "" && c.FallbackURL != c.ManifestURL {
		urls = append(urls, c.FallbackURL)
	}

	var res Result
	var best error
	for _, u := range urls {
		m, err := c.checkURL(u)
		res.Attempts = append(res.Attempts, Attempt{
			URL:    u,
			Status: classifyAttempt(err, m),
			Err:    err,
		})
		if err == nil {
			res.Manifest = m // 可能为 nil：验签通过但没有更高版本
			return res, nil
		}
		best = preferError(best, err)
	}
	return res, best
}

// classifyAttempt 把一次尝试归到 AttemptStatus。
func classifyAttempt(err error, m *Manifest) AttemptStatus {
	if err == nil {
		if m != nil {
			return AttemptNewer
		}
		return AttemptNotNewer
	}
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		return AttemptRejected
	}
	var unusable *UnusableError
	if errors.As(err, &unusable) {
		return AttemptUnusable
	}
	return AttemptUnreachable
}

// preferError 在多个失败里挑一个**更能说明问题**的返回。
//
// 顺序：验签被拒 > 清单不可用 > 其它。
// 为什么：把"配置源清单验签没过"换成"回退源连不上"，会让一次发版事故看起来像网络抖动。
func preferError(cur, next error) error {
	if cur == nil {
		return next
	}
	rank := func(err error) int {
		var rejected *RejectedError
		if errors.As(err, &rejected) {
			return 3
		}
		var unusable *UnusableError
		if errors.As(err, &unusable) {
			return 2
		}
		return 1
	}
	if rank(next) > rank(cur) {
		return next
	}
	return cur
}

func (c *Checker) checkURL(url string) (*Manifest, error) {
	if len(c.PublicKey) == 0 {
		return nil, &UnusableError{URL: url, Err: errors.New("更新公钥未配置")}
	}
	client := c.httpClient(30 * time.Second)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取更新清单失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("解析更新清单失败: %w", err)
	}
	// 验签失败单独一圈类型：它与"连不上"含义完全不同，上层要据此留下痕迹。
	if err := m.Verify(c.PublicKey); err != nil {
		return nil, &RejectedError{URL: url, Err: err}
	}
	if CompareVersions(m.Version, c.CurrentVersion) <= 0 {
		return nil, nil
	}
	if m.MinVersion != "" && CompareVersions(c.CurrentVersion, m.MinVersion) < 0 {
		return nil, &UnusableError{URL: url,
			Err: errors.New("当前版本过低，无法自动更新，请重新安装最新版")}
	}
	if m.URL == "" || m.SHA256 == "" {
		return nil, &UnusableError{URL: url, Err: errors.New("更新清单缺少下载地址或校验值")}
	}
	return &m, nil
}

// Download fetches the update package, verifies its SHA-256 and returns the
// saved path. Large downloads can be flaky, so it retries a few times.
func (c *Checker) Download(m *Manifest, dir string, progress func(done, total int64)) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		path, err := c.downloadOnce(m, dir, progress)
		if err == nil {
			return path, nil
		}
		lastErr = err
		if attempt < 5 {
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	return "", fmt.Errorf("下载失败（已重试 5 次，可能是网络无法连接更新服务器）：%w", lastErr)
}

func (c *Checker) downloadOnce(m *Manifest, dir string, progress func(done, total int64)) (string, error) {
	client := c.httpClient(30 * time.Minute)
	resp, err := client.Get(m.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载更新包失败: HTTP %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = m.Size
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "rferp-update-*.zip")
	if err != nil {
		return "", err
	}
	path := f.Name()
	h := sha256.New()
	var done int64
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(path)
				return "", werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(path)
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, m.SHA256) {
		os.Remove(path)
		return "", errors.New("更新包校验失败：文件哈希与清单不一致")
	}
	return path, nil
}

// FileSHA256 计算一个文件的 SHA-256（小写十六进制）。
//
// 单独导出是因为**同一个校验要做两次**：下载时核对一次，apply 之前再核对一次。
// 写两份实现就会有漂移（一边改成大小写不敏感、另一边没改），而这里的漂移
// 后果是"校验通过的文件"和"真正被替换的文件"不是同一个。
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ApplyAndRestart replaces the running executable with the one inside zipPath
// and launches the new process. The caller is expected to exit afterwards.
func ApplyAndRestart(zipPath string) error {
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	dir := filepath.Dir(target)
	newPath := filepath.Join(dir, "RFERP.new.exe")
	if err := extractExe(zipPath, newPath); err != nil {
		return err
	}
	oldPath := target + ".old"
	os.Remove(oldPath)
	if err := os.Rename(target, oldPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("无法替换程序（安装目录是否可写？）: %w", err)
	}
	if err := os.Rename(newPath, target); err != nil {
		os.Rename(oldPath, target)
		return err
	}
	cmd := exec.Command(target)
	cmd.Env = append(os.Environ(), "RFERP_UPDATE_RESTART=1")
	// 必须脱离当前的作业对象：否则老进程一退出，作业关闭就把这个新进程一起杀掉。
	// 详见 detach_windows.go（2026-10-15 演练抓到的"新版本起来后无声消失"）。
	cmd.SysProcAttr = detachedAttrs()
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

// CleanupOld removes the leftover previous executable after a successful update.
func CleanupOld() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	os.Remove(exe + ".old")
}

// RestartApp launches a new instance of the current executable and returns.
// The caller should os.Exit immediately afterwards so DB handles are released
// before the new process opens the same files (used after SQLite restore).
func RestartApp() error {
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	cmd := exec.Command(target)
	// Reuse the update-restart lock wait so the old process can drop single-instance.
	cmd.Env = append(os.Environ(), "RFERP_UPDATE_RESTART=1")
	// 同上：新进程必须脱离作业对象，否则本进程退出时把它一起带走。
	cmd.SysProcAttr = detachedAttrs()
	return cmd.Start()
}

func extractExe(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		return err
	}
	return errors.New("更新包中未找到可执行文件")
}
