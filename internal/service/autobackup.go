package service

import (
	"fmt"
	"log"
	"time"

	"app/internal/backuplayout"
	"app/internal/config"
	"app/internal/dbbackup"
	"app/internal/paths"
	"app/internal/usecase"
)

// 自动定期备份。
//
// 与手动备份的分工：
//
//	手动（点「一键备份」）→ manual/ → **永不自动删除**
//	自动（按周期）      → auto/   → 滚动保留 paths.AutoBackupKeep 份
//	操作前副本 / 升级前  → safety/ → 各类各保留 paths.SafetyCopyKeep 份
//
// 之所以必须分开目录：早期三者共用 backup_ 前缀、平铺在同一目录，于是
// 「手动永不删」与「副本要滚动清理」这两条相反的策略没法各自成立——
// 前者靠前缀筛选保不住（升级前备份也是 backup_），后者一旦写错前缀就会
// 删掉用户主动留的备份。
//
// 触发时机：应用启动时判一次，运行期由定时器周期判。
// **没有后台服务**，所以「每 n 天」的实际语义是「每 n 天里第一次打开软件时」。
// 这一点必须对用户讲清楚，否则他会以为关掉软件的那天也会备份。

// 状态类型统一用 usecase.BackupSchedule，不在本包另定义一份——
// 两个结构体描述同一件事，改一个忘了另一个就是一次静默的字段漂移。

// AutoBackupDays 返回当前间隔（0 = 关闭）。
func (s *Service) AutoBackupDays() int {
	if s.cfg == nil {
		return 0
	}
	return s.cfg.AutoBackupDays
}

// SetAutoBackupDays 设置间隔；非法值一律拒绝并报错，不静默改成别的值。
//
// 静默兜底（比如把 4 天当成 5 天）是这类设置最糟的失败方式：用户以为每 4 天
// 有一份，实际是每 5 天，而真正需要那份备份的时刻正好落在空档里。
func (s *Service) SetAutoBackupDays(days int) error {
	if !config.ValidAutoBackupDays(days) {
		return fmt.Errorf("不支持的自动备份间隔 %d 天，可选 0（关闭）或 %v", days, config.AllowedAutoBackupDays)
	}
	if s.cfg != nil {
		s.cfg.AutoBackupDays = days
	}
	return nil
}

// AutoBackupStatus 汇总当前自动备份状态。
func (s *Service) AutoBackupStatus() usecase.BackupSchedule {
	days := s.AutoBackupDays()
	st := usecase.BackupSchedule{Enabled: days > 0, Days: days, Keep: paths.AutoBackupKeep}
	if _, ok := s.snapshots.(usecase.PrefixSnapshotPort); !ok {
		st.Reason = "当前存储后端不支持生成备份"
		return st
	}
	if _, dirErr := backuplayout.DirFor(paths.PrefixAuto); dirErr != nil {
		st.Reason = "自动备份目录不可用：" + dirErr.Error()
		return st
	}
	files := listBackupFilesIn(paths.AutoBackupDir())
	st.Count = len(files)
	if len(files) > 0 {
		st.Last = files[0].ModTime.Format("2006-01-02 15:04:05")
		st.NextDue = files[0].ModTime.AddDate(0, 0, days).Format("2006-01-02 15:04:05")
	}
	return st
}

// MaybeAutoBackup 在「该跑的时候」跑一次自动备份，返回是否真的跑了。
//
// 判断依据是 **auto/ 目录里最新一份的时间**，而不是「上次检查的时间」：
// 前者对「软件关了三天再打开」是对的（立刻补一份），后者会让间隔越攒越长。
// 一份都没有时立刻跑一次——开启功能马上就该看到结果，否则用户无法确认
// 它到底工作不工作。
func (s *Service) MaybeAutoBackup(now time.Time) (bool, error) {
	days := s.AutoBackupDays()
	if days <= 0 {
		return false, nil
	}
	port, ok := s.snapshots.(usecase.PrefixSnapshotPort)
	if !ok {
		return false, nil
	}
	dir, err := backuplayout.DirFor(paths.PrefixAuto)
	if err != nil {
		return false, err
	}
	newest, found := newestBackupIn(dir)
	if found && now.Sub(newest) < time.Duration(days)*24*time.Hour {
		return false, nil
	}
	if _, err := port.PrefixSnapshot(dir, paths.PrefixAuto); err != nil {
		// 自动备份失败**不该**惊动用户：它是个后台保底，用户没在等这个结果。
		// 但必须记日志，否则用户发现没有自动备份时无从查起。
		log.Printf("auto backup: failed: %v", err)
		return false, err
	}
	dbbackup.PruneSnapshots(dir, paths.PrefixAuto, paths.AutoBackupKeep)
	log.Printf("auto backup: created in %s (every %d day(s), keep %d)", dir, days, paths.AutoBackupKeep)
	return true, nil
}

// StartAutoBackup 在后台周期检查自动备份。
//
// 间隔取 1 小时：足够及时（打开软件后一小时内一定会判一次），
// 又不至于让一个桌面应用整天空转。真正的备份间隔由 MaybeAutoBackup 按
// 「距最新一份多久」决定，**不是**这个 tick。
func (s *Service) StartAutoBackupTicker(stop <-chan struct{}) {
	if s.AutoBackupDays() <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_, _ = s.MaybeAutoBackup(time.Now())
			}
		}
	}()
}

// newestBackupIn 返回目录里最新的一份备份的时间。
func newestBackupIn(dir string) (time.Time, bool) {
	files := listBackupFilesIn(dir)
	if len(files) == 0 {
		return time.Time{}, false
	}
	return files[0].ModTime, true
}

// listBackupFilesIn 列出某个分类目录里的备份，按新→旧排序。
//
// 直接用 backuplayout.ListDir 而不是遍历整个 List()：它只看该目录的直系子文件。
// 「保留 auto 最近 20 份」这种滚动清理必须以单个目录为准，遍历全部再过滤
// 迟早会因为路径拼接的改动而删错类别的文件。
func listBackupFilesIn(dir string) []backuplayout.Entry {
	entries, err := backuplayout.ListDir(dir)
	if err != nil {
		return nil
	}
	return entries
}
