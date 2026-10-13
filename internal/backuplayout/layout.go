// Package backuplayout 负责备份文件的**物理布局**：哪一类放在哪个目录、
// 旧布局怎么迁移、列表怎么把它们级联出来。
//
// 布局是分类的：不同来源的备份清理策略完全不同（手动永不删、自动保留 20、
// 操作前副本各类保留 5），混在一个目录里就只能靠文件名前缀筛选才能实现，
// 而手动备份与升级前备份同为 backup_ 前缀、策略又不同。
//
// ── 更新兼容性（本包最重要的一条设计约束）──
//
// 旧版本把所有备份平铺在 备份\ 根下。升级到本布局时，那些文件必须**照样能用**。
// 为此有两条硬性要求：
//
//  1. List() 除了遍历三个分类目录，**还必须遍历 备份\ 根目录**。
//     根目录里剩下的文件一律作为「旧布局」类别返回。
//     于是迁移失败、迁移被中断、目录只读、用户手工跳过迁移——任何一种情况
//     都不会让备份在界面上消失。迁移只是整理，不是正确性的前提。
//
//  2. MigrateLegacy 只移动**能确定归类**的文件，且单个文件失败不影响其余；
//     每次移动都写进 备份\layout-migration.log。
//     万一用户之后降级回旧版本，日志里记着每个文件从哪来、到哪去，可以原样搬回。
package backuplayout

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"app/internal/paths"
)

// CategoryLegacy 是「还在 备份\ 根下、没归类」的一类。
//
// 它不是一种备份来源，只是一个兼容容器：旧版本平铺的文件、以及名字不认识的文件
// 都落在这里，直到用户自己整理。界面上要如实标成「旧布局」，不能混进某一类里
// 假装它属于那一类。
const CategoryLegacy paths.BackupCategory = "legacy"

// Entry 是一个备份文件在布局中的位置。
type Entry struct {
	Name     string
	Category paths.BackupCategory
	// Legacy 为 true 表示它还在 备份\ 根下（未迁移）。
	Legacy bool
	// Dir 是它实际所在的目录。调用方要打开文件时用这个，
	// 而不是拿 Category 去拼路径——legacy 项的目录并不是 CategoryDir。
	Dir     string
	Size    int64
	ModTime time.Time
}

// IsBackupExt 报告扩展名是否是可能的备份（mysqldump / SQLite 快照）。
func IsBackupExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".sql", ".db":
		return true
	}
	return false
}

// List 二级级联列出全部备份：先按分类目录，再扫旧布局根目录。
//
// 顺序：分类按 paths.BackupCategories()（auto → safety → manual），
// 每类内新→旧；旧布局永远排在最后，这样新产生的备份不会被历史文件挤到后面。
func List() ([]Entry, error) {
	var out []Entry

	for _, cat := range paths.BackupCategories() {
		got, err := listDir(paths.BackupCategoryDir(cat), cat, false)
		if err != nil {
			// 分类目录还不存在是常态（该类还没产生过备份），不是异常，不记日志。
			// 记了的话每次启动刷三行"找不到目录"，用户会以为出了故障。
			if !os.IsNotExist(err) {
				log.Printf("backuplayout: list %s: %v", cat, err)
			}
			continue
		}
		out = append(out, got...)
	}

	// 旧布局根目录：不能漏。漏了它，迁移一旦失败用户的备份就"消失"了。
	legacy, err := listDir(paths.BackupDir(), CategoryLegacy, true)
	if err == nil {
		out = append(out, legacy...)
	} else if !os.IsNotExist(err) {
		log.Printf("backuplayout: list legacy root: %v", err)
	}

	return out, nil
}

// ListDir 只列一个目录**直接**子级里的备份，按新→旧排序。
//
// 不递归、不认子目录：三个分类目录是平级的，递归进来会把别的类别的备份
// 算成这一类的——滚动清理会因此删错文件（"保留 auto 最近 20 份"却把
// manual 里的删掉）。
//
// 给「滚动清理前先数一数还剩几份」用：那种判断必须以某个目录的直系内容为准。
func ListDir(dir string) ([]Entry, error) {
	return listDir(dir, CategoryLegacy, true)
}

// listDir 列举一个目录里的备份文件。legacyRoot 为 true 时，目录**本身**
// 也在扫描范围内（用于扫 备份\ 根）。
func listDir(dir string, cat paths.BackupCategory, legacyRoot bool) ([]Entry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		// 分类子目录在这里也要被跳过：它们由 paths.BackupCategories() 负责，
		// 否则同一批文件会出现两次（一次带类别、一次当 legacy）。
		if e.IsDir() || !IsBackupExt(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Name:     e.Name(),
			Category: cat,
			Legacy:   legacyRoot,
			Dir:      dir,
			Size:     info.Size(),
			ModTime:  info.ModTime(),
		})
	}
	// 新→旧：用户最可能要恢复的是最近那一份。
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// DirFor 返回某个备份应写入的目录，不保证已存在。
//
// prefix 是文件名前缀（如 paths.PrefixPreClear）；归类以**前缀**为准，
// 不用完整文件名——写文件时名字还没拼出来。
func DirFor(prefix string) (string, error) {
	cat, ok := categoryForPrefix(prefix)
	if !ok {
		return "", fmt.Errorf("未知的备份前缀 %q，无法确定存放目录", prefix)
	}
	dir := paths.BackupCategoryDir(cat)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录 %s: %w", dir, err)
	}
	return dir, nil
}

func categoryForPrefix(prefix string) (paths.BackupCategory, bool) {
	switch prefix {
	case paths.PrefixManual:
		return paths.CategoryManual, true
	case paths.PrefixAuto:
		return paths.CategoryAuto, true
	case paths.PrefixPreRestore, paths.PrefixPreClear, paths.PrefixPreMigrate:
		return paths.CategorySafety, true
	}
	return "", false
}

// Resolve 按文件名在布局里定位一份备份，返回其绝对路径。
//
// 防御与布局无关，两道都要：客户端只能给**文件名**（不能给路径），
// 且解析结果必须仍在某个备份目录内——后者挡 symlink/junction 指向目录外。
// 客户端是 WebView2 里的网页，XSS 或手工构造请求都能到这里。
//
// 搜索顺序与 List() 一致（auto → safety → manual → 旧根），同名取**第一个**命中。
func Resolve(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("请先选择要恢复的备份文件")
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("备份文件必须位于本应用的备份目录内，不能指定路径")
	}
	if !IsBackupExt(name) {
		return "", fmt.Errorf("不是可用的备份文件类型")
	}

	for _, dir := range candidateDirs() {
		target := filepath.Join(dir, name)
		rel, err := filepath.Rel(dir, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		st, err := os.Stat(target)
		if err != nil || st.IsDir() {
			continue
		}
		return target, nil
	}
	return "", fmt.Errorf("备份文件不存在或无法访问，请重新选择")
}

// candidateDirs 是查找顺序，末位是旧布局根目录。
func candidateDirs() []string {
	dirs := make([]string, 0, len(paths.BackupCategories())+1)
	for _, c := range paths.BackupCategories() {
		dirs = append(dirs, paths.BackupCategoryDir(c))
	}
	return append(dirs, paths.BackupDir())
}
