package backuplayout

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"app/internal/paths"
)

// 旧布局 → 分类目录的迁移。
//
// **这是尽力而为的整理，不是正确性的前提。** List() 与 Resolve() 都会照样
// 扫 备份\ 根目录，所以下面每一种情况都不会让用户的备份在界面上消失：
//
//   - 迁移没跑（用户跳过、代码没执行到）
//   - 跑到一半被强杀（下次启动继续，已移走的不会重复处理）
//   - 目录只读 / 权限不足（逐个文件跳过，其余照常）
//   - 分类目录创建失败（该类全部留在原地）
//
// 唯一"不可逆"的情形是用户之后**降级**回旧版本：旧版本只读 备份\ 根目录，
// 看到的是空的。为此每次移动都写进 layout-migration.log，
// 记着每个文件的 from → to，降级后可以照着搬回去。

// MigrationLogName 是迁移日志文件名，放在 备份\ 根下（它自己不会被迁移）。
const MigrationLogName = "layout-migration.log"

// MoveRecord 是一次移动。
type MoveRecord struct {
	From string // 相对备份目录，如 backup_20260924_170830.sql
	To   string // 相对备份目录，如 manual/backup_20260924_170830.sql
}

// MigrationReport 是一次迁移的结果，用于界面提示与测试断言。
type MigrationReport struct {
	Moved   []MoveRecord
	Skipped []string // 留在原地的文件及原因
	DirsErr error    // 分类目录创建失败（若有）
}

// MigrateLegacy 把 备份\ 根下可确定归类的备份文件移进各自的分类目录。
//
// 只移动「能确定归类」的：文件名不符合任何已知前缀的（例如手工命名的
// mybackup.sql）一律留在原地——把不认识的文件挪走等于替用户决定它的含义。
//
// 可重复执行：已经移走的文件不在根目录里，第二次跑是空操作。
// 同名冲突时**保留已存在的那份**并跳过移动：分类目录里同名文件是程序自己写的，
// 根目录里那份更可能是用户手工放的旧文件，覆盖任何一方都可能是错的，
// 这种情形必须让用户自己看一眼。
func MigrateLegacy() (MigrationReport, error) {
	var rep MigrationReport

	root := paths.BackupDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			// 从没备份过的新装：没什么可迁移的，不是错误。
			return rep, nil
		}
		return rep, err
	}

	var logLines []string
	for _, e := range entries {
		if !IsBackupExt(e.Name()) {
			continue
		}
		src := filepath.Join(root, e.Name())
		info, err := os.Lstat(src)
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s（无法检查：%v）", e.Name(), err))
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			rep.Skipped = append(rep.Skipped, e.Name()+"（符号链接保留在原处）")
			continue
		}
		cat, ok := paths.BackupCategoryOf(e.Name())
		if !ok {
			rep.Skipped = append(rep.Skipped, e.Name()+"（文件名无法归类，保留在原处）")
			continue
		}
		dir, err := ensureCategoryDir(cat)
		if err != nil {
			rep.DirsErr = err
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s（无法创建 %s：%v）", e.Name(), cat, err))
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if _, err := os.Lstat(dst); err == nil {
			rep.Skipped = append(rep.Skipped, e.Name()+"（分类目录里已有同名文件，未覆盖）")
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			// 跨卷会失败（备份目录与数据目录通常同卷，但用户可能改过）；
			// 退回复制+删除，至少不会因为跨卷就整个迁移失败。
			if cerr := copyFileThenRemove(src, dst); cerr != nil {
				rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s（移动失败：%v）", e.Name(), err))
				continue
			}
		}
		rep.Moved = append(rep.Moved, MoveRecord{
			From: e.Name(),
			To:   filepath.ToSlash(filepath.Join(string(cat), e.Name())),
		})
		logLines = append(logLines, fmt.Sprintf("%s  %s -> %s/",
			time.Now().Format("2006-01-02 15:04:05"), e.Name(), cat))
	}

	if len(logLines) > 0 {
		writeMigrationLog(logLines)
	}
	return rep, nil
}

func copyFileThenRemove(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	return os.Remove(src)
}

// writeMigrationLog 追加到 备份\layout-migration.log。
//
// 写不进去只记日志不报错：迁移本身已经成功了，日志写不进去不该让启动失败。
// 这份日志唯一的用途是「万一降级，能照着搬回去」，所以宁缺毋滥也不硬塞。
func writeMigrationLog(lines []string) {
	root := paths.BackupDir()
	p := filepath.Join(root, MigrationLogName)
	if _, err := os.Lstat(p); err == nil {
		physical, err := paths.ResolveExistingUnder(paths.BackupDir(), p)
		if err != nil {
			return
		}
		physicalRoot, err := paths.ResolveExistingUnder(paths.BackupDir(), paths.BackupDir())
		if err != nil {
			return
		}
		expected := filepath.Clean(filepath.Join(physicalRoot, MigrationLogName))
		actual := filepath.Clean(physical)
		if runtime.GOOS == "windows" {
			actual, expected = strings.ToLower(actual), strings.ToLower(expected)
		}
		if actual != expected {
			return
		}
	} else if !os.IsNotExist(err) {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	var b strings.Builder
	b.WriteString("# 备份布局迁移日志（升级到分类目录布局时自动生成）\n")
	b.WriteString("# 格式：时间  原文件名 -> 目标分类目录/\n")
	b.WriteString("# 若你之后降级回旧版本，旧版本只读 备份\\ 根目录；照本文件把文件搬回根目录即可。\n")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	_, _ = f.WriteString(b.String())
}
