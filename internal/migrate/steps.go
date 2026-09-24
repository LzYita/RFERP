package migrate

import "github.com/jmoiron/sqlx"

// Step 描述一条「从 N 到 N+1」的可移植迁移（v12 起使用）。
// MySQL 与 SQLite 必须各提供等价 Apply；禁止只写一侧（D-015）。
//
// v1–v11 历史迁移仍走 MySQL 字符串实现；SQLite 空库用 schema_sqlite.go 终态基线。
type Step struct {
	Version int
	Name    string
	MySQL   func(*sqlx.DB) error
	SQLite  func(*sqlx.DB) error
}

// steps 为 v12+ 预留的双端迁移队列。新增迁移时两边都要填。
var steps []Step

// nextVersion 是下一条应新增迁移的版本号。
func nextVersion() int {
	v := CurrentSchemaVersion
	for _, s := range steps {
		if s.Version > v {
			v = s.Version
		}
	}
	return v + 1
}
