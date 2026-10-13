package service

import (
	"path/filepath"

	"app/internal/dbbackup"
	"app/internal/dbfile"
	"app/internal/usecase"
)

// InspectBackupFile 预检一份备份能否安全回滚（实现 usecase.BackupInspector）。
//
// 为什么由 service 提供：判断「缺哪张表」需要知道**当前**有哪些业务表，
// 那份清单是 service 的业务事实（tables.go 的 businessTables），不该复制到 api 或
// dbbackup 里去。dbbackup 只负责读文件、报事实；「哪些表必须被回填」由这里回答。
func (s *Service) InspectBackupFile(path string) (usecase.BackupInspection, error) {
	// SQLite 整库快照是整文件替换（restore.go:59-79），不做「清空 + 回灌」，
	// 因此不存在表级覆盖度问题，也就没有可回填/会丢失之分。
	if dbfile.IsSQLiteSnapshot(path) {
		return usecase.BackupInspection{SchemaKnown: true, Usable: true}, nil
	}

	cov := dbbackup.InspectDump(path, businessTables)
	return usecase.BackupInspection{
		SchemaKnown:   cov.SchemaKnown,
		MissingTables: cov.MissingTables,
		Usable:        cov.Usable,
		Reason:        cov.Reason,
	}, nil
}

var _ usecase.BackupInspector = (*Service)(nil)

// InspectBackupDir 对一个备份目录里的每个候选文件做预检。
//
// 单独提供是因为 api 的列表端点只需要「文件名 + 预检结论」，
// 不该为了拼结论去知道绝对路径是怎么拼出来的（备份目录来自用户可配置的数据目录）。
func (s *Service) InspectBackupDir(dir string, names []string) map[string]usecase.BackupInspection {
	out := make(map[string]usecase.BackupInspection, len(names))
	for _, n := range names {
		insp, err := s.InspectBackupFile(filepath.Join(dir, n))
		if err != nil {
			out[n] = usecase.BackupInspection{Usable: false, Reason: "备份文件无法读取"}
			continue
		}
		out[n] = insp
	}
	return out
}
