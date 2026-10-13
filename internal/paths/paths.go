package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var dataDir string

// ResolveUnder checks existing filesystem targets, including symlinks and junctions.
// shortcut: paths can change after validation; use rooted handles for hostile filesystems.
func ResolveUnder(root, target string) (string, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filesystemPath(root)
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	target, err = filesystemPath(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径不在指定目录内")
	}
	return target, nil
}

func SetDataDir(d string) {
	dataDir = d
}

func DataDir() string {
	if dataDir != "" {
		return dataDir
	}
	if v := os.Getenv("MES_DATA_DIR"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "RFERP")
	}
	return "RFERP"
}

func BackupDir() string {
	return filepath.Join(DataDir(), "备份")
}

func ExportDir() string {
	return filepath.Join(DataDir(), "导出")
}

// 备份的**分类目录**。
//
// 为什么分类：不同来源的备份清理策略完全不同，混在一个目录里就没法各自处理。
// 早期所有备份都堆在 备份\ 根下，于是「操作前副本保留 5 份」这条规则必须靠
// 文件名前缀筛选才能实现，而手动备份与迁移前备份同为 backup_ 前缀、清理策略
// 又不同（前者永不删、后者要滚动），分类之后这两件事各自落在自己的目录里。
//
// 目录名用英文：这些路径会被命令行工具、脚本与将来的服务器端工具直接引用，
// 英文名不必担心编码与转义。
type BackupCategory string

const (
	// CategoryManual 用户手动「一键备份」的产物。**永不自动删除**——
	// 用户主动留的东西，任何"清理旧文件"都不该替用户决定。
	CategoryManual BackupCategory = "manual"
	// CategoryAuto 按设定周期自动产生的备份，滚动保留 AutoBackupKeep 份。
	CategoryAuto BackupCategory = "auto"
	// CategorySafety 破坏性操作与升级前自动生成的兜底副本，
	// 各前缀分别滚动保留 SafetyCopyKeep 份。
	CategorySafety BackupCategory = "safety"
)

// 各类备份的文件名前缀。分类靠它判定，因此改这里等于改历史兼容性。
const (
	PrefixManual     = "backup"
	PrefixAuto       = "auto"
	PrefixPreRestore = "pre_restore"
	PrefixPreClear   = "pre_clear"
	PrefixPreMigrate = "pre_migrate"
)

// 滚动保留份数。
//
//	手动：无限（不清理）
//	自动：20 份
//	操作前副本：每类各 5 份
const (
	AutoBackupKeep = 20
	SafetyCopyKeep = 5
)

// BackupCategoryDir 返回某类备份的目录（不保证已存在）。
func BackupCategoryDir(c BackupCategory) string {
	return filepath.Join(BackupDir(), string(c))
}

// ManualBackupDir / AutoBackupDir / SafetyBackupDir 是三处高频调用的便捷写法。
func ManualBackupDir() string { return BackupCategoryDir(CategoryManual) }
func AutoBackupDir() string   { return BackupCategoryDir(CategoryAuto) }
func SafetyBackupDir() string { return BackupCategoryDir(CategorySafety) }

// BackupCategories 是列表与遍历的固定顺序：先看最新的自动备份，
// 再看兜底副本，最后是用户手动留的。与界面下拉框的分组顺序一致。
func BackupCategories() []BackupCategory {
	return []BackupCategory{CategoryAuto, CategorySafety, CategoryManual}
}

// BackupCategoryOf 按文件名前缀判定它属于哪一类。
//
// 顺序敏感：前缀互不为前缀（backup / auto / pre_restore / pre_clear / pre_migrate），
// 但要显式列出而不是「取第一个匹配的前缀」，否则将来新增前缀时的顺序会
// 悄悄改变历史文件的归类——那是一次静默的数据移动。
//
// 第二个返回值 false 表示「不认识」：那种文件不归任何一类，迁移时留在原地。
func BackupCategoryOf(name string) (BackupCategory, bool) {
	base := strings.TrimSpace(name)
	switch {
	case hasBackupTimestamp(base, PrefixAuto):
		return CategoryAuto, true
	case hasBackupTimestamp(base, PrefixPreRestore),
		hasBackupTimestamp(base, PrefixPreClear),
		hasBackupTimestamp(base, PrefixPreMigrate):
		return CategorySafety, true
	case hasBackupTimestamp(base, PrefixManual):
		return CategoryManual, true
	}
	return "", false
}

// hasBackupTimestamp 判断 name 是否为 <prefix>_<时间戳>[.限定词].sql|.db。
//
// 这里刻意与 internal/api 的 restoreFilePattern 同形但**不共享代码**：
// api 那个回答「能不能作为恢复来源」，这里回答「存哪个目录」，
// 两者面对的提问不同——但它们必须对同一批文件给出相同的分类，
// 所以 backup_layout_test.go 用真实文件名把两者锁在一起。
func hasBackupTimestamp(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix+"_") {
		return false
	}
	rest := name[len(prefix)+1:]
	// 去掉一个可选的点分段（.final / .upgraded 之类）与结尾扩展名。
	dot := strings.IndexByte(rest, '.')
	if dot >= 0 {
		rest = rest[:dot]
	}
	return len(rest) == len("20060102_150405") && isDigits(rest[:8]) && rest[8] == '_' && isDigits(rest[9:])
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
