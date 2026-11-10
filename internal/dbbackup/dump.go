package dbbackup

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// ErrDumpToolNotFound 表示本机找不到可用的 mysqldump。
//
// 单独成类型而不是一段字符串：升级失败时调用方要据此弹「选择 mysqldump.exe」
// 的对话框，而不是只把错误念给用户听。
var ErrDumpToolNotFound = errors.New("未找到 mysqldump（升级前备份需要本机 MySQL 客户端）")

// resolveDumpPath 解析实际要执行的 mysqldump。
//
// 优先级：配置的完整路径 → 配置的命令名（PATH 里找）→ PATH 里的 mysqldump。
//
// 为什么不能只依赖 PATH：桌面程序由资源管理器启动，继承的是登录时的环境变量；
// 很多机器的 MySQL bin 目录从未进过 PATH，而 config.json 里的 mysqldumpPath
// 又是首启向导之后才有的字段。2026-11-10 的升级事故就是这条链断了：
// 备份失败 → 迁移中止 → 新版本起不来。
//
// 自动发现（服务注册表/运行进程/安装目录）在 bootstrap 里做，因为那里才有
// 完整配置；这里只兜底 PATH，并给出可操作的错误。
func resolveDumpPath(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if st, err := os.Stat(configured); err == nil && !st.IsDir() {
			return configured, nil
		}
		// 兼容只写命令名的旧配置。
		if !strings.ContainsAny(configured, `/\`) {
			if p, err := exec.LookPath(configured); err == nil {
				return p, nil
			}
		}
		log.Printf("dbbackup: 配置的 mysqldump 不可用，改为在 PATH 中查找: %s", configured)
	}
	if p, err := exec.LookPath("mysqldump"); err == nil {
		return p, nil
	}
	hint := "请安装 MySQL 客户端，或在 config.json 的 mysqldumpPath 指定 mysqldump.exe 的完整路径"
	if configured != "" {
		return "", fmt.Errorf("%w：配置的路径不可用（%s）。%s", ErrDumpToolNotFound, configured, hint)
	}
	return "", fmt.Errorf("%w：%s", ErrDumpToolNotFound, hint)
}

// dumpArgs 是 mysqldump 的固定参数。
//
// --single-transaction 有两个理由：
//   - 不拿 LOCK TABLES 权限：首启向导创建的专用账号只授了 SELECT/DML/DDL，
//     没有 LOCK TABLES；默认模式会在备份第一步就 Access denied。
//   - InnoDB 表拿到一致性快照，备份期间业务写入不被阻塞。
func dumpArgs(cfgFile, dbName string) []string {
	return []string{"--defaults-extra-file=" + cfgFile, "--single-transaction", dbName}
}

// dumpFailure 把 mysqldump 的 stderr 带进错误里。
//
// 原先 cmd.Stderr 直接接 os.Stderr，而宿主是 GUI 程序（-H windowsgui），
// 那段输出没有任何人看得到；用户与支持拿到的只有 "exit status 1"。
// 权限不足、磁盘满、版本不兼容这些真实原因都在 stderr 里，必须留在错误上。
func dumpFailure(runErr error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		return fmt.Errorf("mysqldump failed: %w", runErr)
	}
	// 只留尾部：真正的错误通常在最后，前面的警告没有排查价值。
	if r := []rune(detail); len(r) > 600 {
		detail = string(r[len(r)-600:])
	}
	return fmt.Errorf("mysqldump failed: %w：%s", runErr, detail)
}
