package migrate

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
)

// RunSQLite 将 SQLite 库迁到 CurrentSchemaVersion。
//
// 策略（D-015）：
//   - 空库：应用 v11 终态基线，并记 schema_migrations = 1..11
//   - 已在 v11：无事可做
//   - 低于 v11 且已有业务表：不支持从历史半成品升级（Local 新库路径专用），明确报错
//   - 高于 11 的 pending：按 steps 双端描述执行
//
// 不做 mysqldump 备份；文件级快照由 SnapshotPort（PR3）负责。
func RunSQLite(db *sqlx.DB) (Result, error) {
	var res Result
	if err := execAll(db, []string{sqliteSchemaMigrationsDDL}); err != nil {
		return res, err
	}
	cur, err := currentVersionSQL(db)
	if err != nil {
		return res, err
	}
	res.FromVersion = cur

	if cur == 0 {
		empty, err := sqliteDatabaseEmpty(db)
		if err != nil {
			return res, err
		}
		if empty {
			log.Printf("migrate/sqlite: applying baseline v%d", CurrentSchemaVersion)
			if err := applySQLiteBaseline(db); err != nil {
				return res, fmt.Errorf("sqlite baseline: %w", err)
			}
			for _, v := range sqliteBaselineVersions {
				if err := recordVersionSQL(db, v.Version, v.Name); err != nil {
					return res, fmt.Errorf("record v%d: %w", v.Version, err)
				}
				res.Applied = append(res.Applied, v.Version)
			}
			res.ToVersion = CurrentSchemaVersion
			return res, nil
		}
		return res, fmt.Errorf("sqlite: database has tables but schema_migrations is empty; refuse to guess history")
	}

	if cur < CurrentSchemaVersion {
		return res, fmt.Errorf("sqlite: version %d is below baseline %d and is not a supported upgrade path; use a new file or export/import", cur, CurrentSchemaVersion)
	}

	var pending []Step
	for _, s := range steps {
		if s.Version > cur && s.SQLite != nil {
			pending = append(pending, s)
		}
	}
	for _, s := range pending {
		log.Printf("migrate/sqlite: applying v%d %s", s.Version, s.Name)
		if err := s.SQLite(db); err != nil {
			return res, fmt.Errorf("migration v%d (%s) failed: %w", s.Version, s.Name, err)
		}
		if err := recordVersionSQL(db, s.Version, s.Name); err != nil {
			return res, fmt.Errorf("record v%d: %w", s.Version, err)
		}
		res.Applied = append(res.Applied, s.Version)
	}
	if len(pending) > 0 {
		res.ToVersion = pending[len(pending)-1].Version
	} else {
		res.ToVersion = cur
	}
	return res, nil
}

func applySQLiteBaseline(db *sqlx.DB) error {
	return execAll(db, sqliteBaselineV11)
}

func execAll(db *sqlx.DB, stmts []string) error {
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", firstLine(stmt), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// currentVersionSQL 在 MySQL / SQLite 上都可用（无 information_schema）。
func currentVersionSQL(db *sqlx.DB) (int, error) {
	var v sql.NullInt64
	if err := db.Get(&v, `SELECT MAX(version) FROM schema_migrations`); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

func recordVersionSQL(db *sqlx.DB, version int, name string) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (?, ?)`, version, name)
	return err
}

func sqliteDatabaseEmpty(db *sqlx.DB) (bool, error) {
	var n int
	err := db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name <> 'schema_migrations'`)
	return n == 0, err
}
