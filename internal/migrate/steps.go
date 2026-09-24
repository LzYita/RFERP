package migrate

import "github.com/jmoiron/sqlx"

// Step 描述一条可移植迁移（v12 起使用）。
// MySQL 与 SQLite 必须各提供等价 Apply；禁止只写一侧（D-015）。
//
// v1–v11 历史迁移仍走 MySQL 字符串实现；SQLite 空库用 schema_sqlite.go 终态基线。
type Step struct {
	Version int
	Name    string
	MySQL   func(*sqlx.DB) error
	SQLite  func(*sqlx.DB) error
}

// steps 为 v12+ 的双端迁移队列。新增迁移时两边都要填（D-015）。
//
// v12：恢复 MySQL products.code 唯一约束（v4 误删）。SQLite 基线已带 UNIQUE。
var steps = []Step{
	{
		Version: 12,
		Name:    "products_code_unique",
		MySQL:   applyMySQLProductsCodeUnique,
		SQLite:  assertSQLiteProductsCodeUnique,
	},
}

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

func applyMySQLProductsCodeUnique(db *sqlx.DB) error {
	// v1 曾有 UNIQUE KEY `code`，v4 DROP INDEX code 删除了它。此处恢复。
	ok, err := indexExists(db, "products", "uq_products_code")
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	ok, err = indexExists(db, "products", "code")
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE products ADD UNIQUE KEY uq_products_code (code)`)
	return err
}

func assertSQLiteProductsCodeUnique(db *sqlx.DB) error {
	// 基线已声明 code UNIQUE；空库探测不可靠时接受列定义。
	// 有数据则试插重复编码，必须失败。
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM products`); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	var code string
	if err := db.Get(&code, `SELECT code FROM products LIMIT 1`); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO products (code,name) VALUES (?, 'dup-probe')`, code)
	if err == nil {
		return errUniqueNotEnforced
	}
	return nil
}

var errUniqueNotEnforced = errUnique("products.code UNIQUE is not enforced")

type errUnique string

func (e errUnique) Error() string { return string(e) }
