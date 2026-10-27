package migrate

import (
	"testing"

	"github.com/jmoiron/sqlx"
)

// v15 逐列补齐：迁移中途失败重跑时可能只落了部分列。只看 use_mode
// 就收手会让缺失的列永远补不上，所以这里直接构造"只有 use_mode"的
// 半成品表，断言另外两列会被补上、且重复执行是幂等的。
func TestTempPartsDerivationSQLiteFillsPartialColumns(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := db.Exec(`CREATE TABLE batch_temp_parts (
		id INTEGER PRIMARY KEY,
		batch_id INTEGER NOT NULL,
		part_id INTEGER NOT NULL,
		qty NUMERIC NOT NULL,
		created_at DATETIME,
		use_mode SMALLINT NULL
	)`); err != nil {
		t.Fatalf("seed partial table: %v", err)
	}

	if err := applyTempPartsDerivationSQLite(db); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	have := tempPartColumns(t, db)
	for _, col := range []string{"use_mode", "quantity", "loss_rate"} {
		if !have[col] {
			t.Fatalf("列 %s 缺失（实际：%v）", col, have)
		}
	}

	// 再跑一次不能因为"列已存在"失败。
	if err := applyTempPartsDerivationSQLite(db); err != nil {
		t.Fatalf("second apply (idempotent): %v", err)
	}
}

func tempPartColumns(t *testing.T, db *sqlx.DB) map[string]bool {
	t.Helper()
	rows, err := db.Queryx(`PRAGMA table_info(batch_temp_parts)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dft any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dft, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		have[name] = true
	}
	return have
}
