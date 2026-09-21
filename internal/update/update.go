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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Checker struct {
	ManifestURL    string
	CurrentVersion string
	PublicKey      ed25519.PublicKey
	Client         *http.Client
}

func NewChecker(manifestURL, currentVersion string) (*Checker, error) {
	pub, err := PublicKey()
	if err != nil {
		return nil, err
	}
	return &Checker{
		ManifestURL:    manifestURL,
		CurrentVersion: currentVersion,
		PublicKey:      pub,
		Client:         &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Check returns a manifest when a newer, signed version is available; nil when
// there is nothing to do.
func (c *Checker) Check() (*Manifest, error) {
	if len(c.PublicKey) == 0 {
		return nil, errors.New("更新公钥未配置")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Get(c.ManifestURL)
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
	if err := m.Verify(c.PublicKey); err != nil {
		return nil, err
	}
	if CompareVersions(m.Version, c.CurrentVersion) <= 0 {
		return nil, nil
	}
	if m.MinVersion != "" && CompareVersions(c.CurrentVersion, m.MinVersion) < 0 {
		return nil, fmt.Errorf("当前版本过低，无法自动更新，请重新安装最新版")
	}
	if m.URL == "" || m.SHA256 == "" {
		return nil, errors.New("更新清单缺少下载地址或校验值")
	}
	return &m, nil
}

// Download fetches the update package, verifies its SHA-256 and returns the
// saved path. Large downloads can be flaky, so it retries a few times.
func (c *Checker) Download(m *Manifest, dir string, progress func(done, total int64)) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		path, err := c.downloadOnce(m, dir, progress)
		if err == nil {
			return path, nil
		}
		lastErr = err
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	return "", lastErr
}

func (c *Checker) downloadOnce(m *Manifest, dir string, progress func(done, total int64)) (string, error) {
	// Use a dedicated client with a long timeout: update packages are large and
	// the manifest-check client's short timeout is unsuitable for them.
	client := &http.Client{Timeout: 30 * time.Minute}
	if c.Client != nil && c.Client.Transport != nil {
		client.Transport = c.Client.Transport
	}
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
	if err := exec.Command(target).Start(); err != nil {
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
