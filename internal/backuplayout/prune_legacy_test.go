package backuplayout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/dbbackup"
	"app/internal/paths"
)

// legacy 备份不被任何剪枝策略删除（Round 8 先决项③，2026-10-15 补的断言）。
//
// 背景：1.3.0 写 `备份\backup_<ts>.sql`，与今天的手动备份同为 `backup_` 前缀。
// 分类目录就是为了让"用户主动留的东西"不被别的策略顺手清掉——但这原本**只是注释里
// 的承诺**（paths.go 的 CategoryManual「永不自动删除」、restore.go 的 pruneSafetyCopies
// 注释），没有任何断言守着。哪天有人把白名单扩成"按 backup 前缀清"，或者把
// MigrateLegacy 改成"清掉根下剩余文件"，都不会有测试变红，而老用户的备份就没了。
//
// 这里守的是**跨层契约**：List 会把 legacy 文件列出来，而四条剪枝路径一条都不许碰它。
//
// 四条剪枝路径（全部枚举，不猜）：
//  1. autoBackup 滚动清理（prefix=auto，在 备份\auto\ 里）
//  2. 恢复/清空前的兜底副本滚动清理（prefix ∈ {pre_restore, pre_clear, pre_migrate}，
//     各在 备份\safety\<prefix>\ 里）
//  3. 升级前快照滚动清理（prefix=pre_migrate，备份\safety\ 下）
//  4. MigrateLegacy 的搬迁（只按前缀搬，搬不动的留下）
func TestPruningNeverDeletesLegacyBackups(t *testing.T) {
	setup(t)

	// 1.3.0 留下的三种典型 legacy 文件：手动备份、升级前快照、以及一个认不出的名字。
	//
	// 时间戳**故意用旧的**（2025 年）：如果哪天策略漏到 备份\ 根目录，
	// PruneSnapshots 会按"保留最新 keep 个"把它们删掉——但只有旧文件真的落在
	// 会被删的那一侧。用了新时间戳的话它们会混进"最新 keep 个"里被顺手救下来，
	// 断言就成了假阳性（这一点是写反��验证脚本时才发现的）。
	legacyNames := []string{
		"backup_20250101_000000.sql", // 手动备份（backup_ 前缀，最容易被误伤）
		"pre_migrate_20250102_000000.sql",
		"rferp_manual_copy.sql", // 认不出的前缀
	}
	for _, n := range legacyNames {
		put(t, paths.BackupDir(), n, 32)
	}
	// 同一分类目录里塞满可清理的同类文件，超过保留上限，逼策略真的动手
	const overflow = paths.SafetyCopyKeep + 4
	for i := 0; i < overflow; i++ {
		put(t, paths.BackupDir(), legacyName("pre_migrate", i), 32)
		put(t, paths.BackupDir(), legacyName("pre_restore", i), 32)
		put(t, paths.BackupDir(), legacyName("pre_clear", i), 32)
	}
	// auto_ 也要放满：验证 autoBackup 的滚动清理不会越过目录去动根目录
	autoDir, err := DirFor(paths.PrefixAuto)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < paths.AutoBackupKeep+4; i++ {
		put(t, autoDir, legacyName("auto", i), 32)
	}

	// ---- 逐条跑真实的剪枝入口 ----
	// 2/3. 恢复与清空前的兜底副本清理（restore.go 的 pruneSafetyCopies 走的就是它）
	for _, prefix := range []string{paths.PrefixPreRestore, paths.PrefixPreClear, paths.PrefixPreMigrate} {
		dir, err := DirFor(prefix)
		if err != nil {
			t.Fatalf("%s 的分类目录不可用：%v", prefix, err)
		}
		dbbackup.PruneSnapshots(dir, prefix, paths.SafetyCopyKeep)
	}
	// 1. 自动备份滚动清理
	dbbackup.PruneSnapshots(autoDir, paths.PrefixAuto, paths.AutoBackupKeep)

	// 4. 升级前的快照清理（bootstrap.go 单独调的那一次）
	if safetyDir, err := DirFor(paths.PrefixPreMigrate); err == nil {
		dbbackup.PruneSnapshots(safetyDir, paths.PrefixPreMigrate, paths.SafetyCopyKeep)
	}

	// 断言：legacy 的三个文件必须都还在
	for _, n := range legacyNames {
		if _, err := os.Stat(filepath.Join(paths.BackupDir(), n)); err != nil {
			t.Errorf("剪枝删掉了 legacy 备份 %q：%v\n用户主动留下的东西任何清理策略都不该替用户决定（paths.CategoryManual）", n, err)
		}
	}

	// 而且必须仍然**能被界面看到** —— 文件还在但 List 不认，等于用户找不到，等于没保住
	got := names(t)
	for _, n := range legacyNames {
		if _, ok := got[n]; !ok {
			t.Errorf("legacy 备份 %q 还在磁盘上，但 List() 不再列出它：界面上会看不到，等于丢失", n)
		}
	}
}

// 剪枝确实该发生的地方也要确认，否则上面那条可能只是"策略根本没跑"。
// 没有这一条，"legacy 还在"可能只是因为剪枝整体失灵。
func TestPruningStillDeletesItsOwnFiles(t *testing.T) {
	setup(t)

	autoDir, err := DirFor(paths.PrefixAuto)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < paths.AutoBackupKeep+6; i++ {
		put(t, autoDir, legacyName("auto", i), 32)
	}
	// 一份手动备份放在同一目录里做对照
	put(t, autoDir, "backup_20260101_000000.sql", 32)

	removed := dbbackup.PruneSnapshots(autoDir, paths.PrefixAuto, paths.AutoBackupKeep)
	if len(removed) == 0 {
		t.Fatal("auto_ 文件超出保留上限却没有删任何东西：上一条测试可能只是因为剪枝失灵才通过")
	}
	for _, n := range removed {
		if !strings.HasPrefix(n, paths.PrefixAuto+"_") {
			t.Errorf("剪枝删掉了非 auto_ 前缀的文件 %q：它只该清自己那一类", n)
		}
	}
	// 对照组：同目录下的 backup_ 前缀文件不受影响
	if _, err := os.Stat(filepath.Join(autoDir, "backup_20260101_000000.sql")); err != nil {
		t.Errorf("auto_ 的滚动清理误删了 backup_ 前缀的文件：%v", err)
	}
}

// legacyName 造一个带序号的时间戳文件名，保证字典序 = 时间序（PruneSnapshots 依赖这一点）。
//
// 序号要**补零到固定宽度**：PruneSnapshots 靠 sort.Strings 认时间序，
// 而 autoBackupKeep 是 20，两个以上位数就不同了，排序与"新旧"就对不上，
// 于是该删的没删——那正是下面那条对照断言要抓的现象，不能自己先踩进去。
func legacyName(prefix string, i int) string {
	return prefix + "_20260101_" + twoDigits(i) + "0000.sql"
}

func twoDigits(i int) string {
	return string([]byte{byte('0' + (i/10)%10), byte('0' + i%10)})
}
