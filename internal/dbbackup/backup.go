package dbbackup

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Backup 生成一份完整 mysqldump 备份，文件名 backup_<时间戳>.sql。
// 行为被 #28 的升级前备份复用，不可加任何过滤参数。
func Backup(dsn, mysqldumpPath, saveDir string) (string, error) {
	return BackupTo(dsn, mysqldumpPath, saveDir, "backup")
}

// BackupTo 与 Backup 相同，但文件名使用自定义前缀（前缀_时间戳.sql）。
// 供「破坏性操作前的自动副本」区分用途：pre_restore / pre_clear。
// 产物仍是完整 dump，可直接作为另一次 BackupTo 的输入。
func BackupTo(dsn, mysqldumpPath, saveDir, prefix string) (string, error) {
	if prefix == "" {
		prefix = "backup"
	}
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	if mysqldumpPath == "" {
		mysqldumpPath = "mysqldump"
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	cfgFile, err := writeClientConfig(mc)
	if err != nil {
		return "", err
	}
	defer os.Remove(cfgFile)

	now := time.Now().Format("20060102_150405")
	filePath := filepath.Join(saveDir, fmt.Sprintf("%s_%s.sql", prefix, now))

	cmd := exec.Command(mysqldumpPath, "--defaults-extra-file="+cfgFile, mc.DBName)
	outFile, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("create backup file: %w", err)
	}
	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	closeErr := outFile.Close()
	if runErr != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("mysqldump failed: %w", runErr)
	}
	if closeErr != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("close backup file: %w", closeErr)
	}
	// 校验：mysqldump 退出码为 0 也可能留下被截断的文件（例如磁盘写满）。
	// 调用方会据此决定是否继续升级，所以这里必须确认 dump 完整。
	if err := VerifyDumpFile(filePath); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("backup verification failed: %w", err)
	}
	return filePath, nil
}

// PruneSnapshots 在 saveDir 下只保留最新的 keep 个 <prefix>_*.sql。
// 用于限制「破坏性操作前自动副本」的份数。清理失败只记日志式忽略，
// 绝不返回错误：副本已经生成，清理不应影响调用方的结果。
func PruneSnapshots(saveDir, prefix string, keep int) []string {
	if keep < 0 {
		keep = 0
	}
	entries, err := os.ReadDir(saveDir)
	if err != nil {
		return nil
	}
	var names []string
	want := prefix + "_"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, want) && strings.HasSuffix(strings.ToLower(n), ".sql") {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return nil
	}
	// 文件名内嵌固定格式时间戳，字典序即时间序。
	sort.Strings(names)
	var removed []string
	for _, n := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(saveDir, n)); err == nil {
			removed = append(removed, n)
		}
	}
	return removed
}

// dumpCompleteMarker 是 mysqldump 在成功结束时写入的最后一行标记。
const dumpCompleteMarker = "Dump completed"

// VerifyDumpFile 确认备份文件非空且带有 mysqldump 的完成标记。
// 只读文件尾部，避免把整个 dump 读进内存。
// 恢复前的闸门也复用它：缺标记说明文件被截断（例如磁盘写满），
// 拿它去覆盖数据只会得到一个残缺的库。
func VerifyDumpFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("备份文件为空")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	const tailSize = 4096
	start := info.Size() - tailSize
	if start < 0 {
		start = 0
	}
	buf := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return err
	}
	if !bytes.Contains(buf, []byte(dumpCompleteMarker)) {
		return fmt.Errorf("备份文件不完整（缺少 %q 标记）", dumpCompleteMarker)
	}
	return nil
}

func writeClientConfig(mc *mysql.Config) (string, error) {
	f, err := os.CreateTemp("", "rferp-mysql-*.cnf")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}
	name := f.Name()

	var b strings.Builder
	b.WriteString("[client]\n")
	b.WriteString("user=" + cnfQuote(mc.User) + "\n")
	b.WriteString("password=" + cnfQuote(mc.Passwd) + "\n")
	if host, port, ok := splitHostPort(mc.Addr); ok {
		b.WriteString("host=" + cnfQuote(host) + "\n")
		b.WriteString("port=" + cnfQuote(port) + "\n")
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(name)
		return "", fmt.Errorf("write temp config: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("close temp config: %w", err)
	}
	return name, nil
}

func cnfQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func splitHostPort(addr string) (string, string, bool) {
	if addr == "" {
		return "", "", false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, "", false
	}
	return host, port, true
}
