//go:build !windows

package mysqlfind

import (
	"os"
	"os/exec"
	"strings"
)

// FindMysqldump 在非 Windows 平台上只认配置值与 PATH。
//
// 服务/注册表/进程路径那套发现逻辑是 Windows 专用的；Linux 服务器上
// MySQL 客户端要么在 PATH，要么由运维显式配置，保持简单更可预期。
func FindMysqldump(configured, _ string) (string, []string) {
	var tried []string
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if strings.ContainsAny(configured, `/\`) {
			tried = append(tried, configured)
			if st, err := os.Stat(configured); err == nil && !st.IsDir() {
				return configured, tried
			}
		} else if p, err := exec.LookPath(configured); err == nil {
			return p, append(tried, configured)
		} else {
			tried = append(tried, configured)
		}
	}
	if p, err := exec.LookPath("mysqldump"); err == nil {
		return p, append(tried, "PATH")
	}
	return "", append(tried, "PATH")
}
