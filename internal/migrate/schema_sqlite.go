package migrate

// SQLite v11 终态基线（D-015）：空库一次建到 v11，schema_migrations 记 1..11。
// 与 MySQL 历史迁移 v1–v11 语义对齐；方言差异只出现在本文件。
//
// 约定：
//   - 自增：INTEGER PRIMARY KEY（rowid 别名）
//   - 时间：DATETIME 存 RFC3339 UTC（strftime），可被 parseTime 扫入 time.Time
//   - JSON 列：TEXT
//   - DECIMAL：NUMERIC（量化规则在 Service）
//   - updated_at 不做 ON UPDATE 自动更新，由写路径显式赋值（与部分 MySQL 路径一致）
//
// products.code 唯一性（评审结论）：**保留 UNIQUE**。目录编码必须唯一。
// MySQL v1 曾有 UNIQUE，v4 applyProductsDropCodeIndex 误删了该唯一约束；
// 历史库可能已出现重复编码。双端契约按「code 唯一」执行；MySQL 侧用 v12
// 恢复唯一索引（见 steps），SQLite 基线直接带上 UNIQUE。

const sqliteSchemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER NOT NULL PRIMARY KEY,
	name       TEXT    NOT NULL,
	applied_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
)`

// sqliteBaselineV11 表名顺序满足外键引用。
var sqliteBaselineV11 = []string{
	`CREATE TABLE IF NOT EXISTS products (
	id         INTEGER PRIMARY KEY,
	code       TEXT    NOT NULL UNIQUE,
	name       TEXT    NOT NULL,
	spec       TEXT,
	unit       TEXT    NOT NULL DEFAULT '个',
	status     INTEGER NOT NULL DEFAULT 1,
	version    INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	operator   TEXT
)`,
	`CREATE TABLE IF NOT EXISTS parts (
	id         INTEGER PRIMARY KEY,
	code       TEXT    NOT NULL UNIQUE,
	name       TEXT    NOT NULL,
	spec       TEXT,
	unit       TEXT    NOT NULL DEFAULT '个',
	part_type  TEXT,
	stock_qty  NUMERIC NOT NULL DEFAULT 0,
	warn_qty   NUMERIC NOT NULL DEFAULT 0,
	-- ck_parts_stock_nonnegative / ck_parts_warn_nonnegative (MySQL v10)
	status     INTEGER NOT NULL DEFAULT 1,
	version    INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	operator   TEXT,
	supplier   TEXT,
	CHECK (stock_qty >= 0),
	CHECK (warn_qty >= 0)
)`,
	`CREATE TABLE IF NOT EXISTS bom_items (
	id         INTEGER PRIMARY KEY,
	product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
	part_id    INTEGER NOT NULL REFERENCES parts(id),
	quantity   NUMERIC NOT NULL,
	loss_rate  NUMERIC NOT NULL DEFAULT 0,
	remark     TEXT,
	version    INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	operator   TEXT,
	replaceable INTEGER NOT NULL DEFAULT 0,
	use_mode    INTEGER NOT NULL DEFAULT 0,
	UNIQUE (product_id, part_id),
	CHECK (quantity > 0),
	CHECK (loss_rate >= 0 AND loss_rate <= 100)
)`,
	`CREATE TABLE IF NOT EXISTS product_batches (
	id                   INTEGER PRIMARY KEY,
	batch_no             TEXT    NOT NULL UNIQUE,
	product_id           INTEGER NOT NULL REFERENCES products(id),
	plan_qty             INTEGER NOT NULL,
	produced_qty         INTEGER NOT NULL DEFAULT 0,
	status               INTEGER NOT NULL DEFAULT 0,
	version              INTEGER NOT NULL DEFAULT 1,
	created_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	operator             TEXT,
	customer             TEXT,
	consumption_recorded INTEGER NOT NULL DEFAULT 0,
	CHECK (plan_qty > 0)
)`,
	`CREATE TABLE IF NOT EXISTS batch_skip_parts (
	id         INTEGER PRIMARY KEY,
	batch_id   INTEGER NOT NULL REFERENCES product_batches(id) ON DELETE CASCADE,
	part_id    INTEGER NOT NULL REFERENCES parts(id),
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	UNIQUE (batch_id, part_id)
)`,
	`CREATE TABLE IF NOT EXISTS batch_trace (
	id           INTEGER PRIMARY KEY,
	batch_id     INTEGER NOT NULL REFERENCES product_batches(id),
	part_id      INTEGER NOT NULL REFERENCES parts(id),
	part_batch_no TEXT,
	used_qty     NUMERIC NOT NULL,
	supplier     TEXT,
	created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	operator     TEXT,
	CHECK (used_qty > 0)
)`,
	`CREATE TABLE IF NOT EXISTS batch_consumptions (
	id           INTEGER PRIMARY KEY,
	batch_id     INTEGER NOT NULL REFERENCES product_batches(id) ON DELETE CASCADE,
	part_id      INTEGER NOT NULL REFERENCES parts(id),
	consumed_qty NUMERIC NOT NULL,
	created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	UNIQUE (batch_id, part_id),
	CHECK (consumed_qty >= 0)
)`,
	`CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY,
	username      TEXT    NOT NULL UNIQUE,
	password_hash TEXT    NOT NULL,
	display_name  TEXT,
	role          TEXT    NOT NULL DEFAULT 'viewer',
	status        INTEGER NOT NULL DEFAULT 1,
	last_login_at DATETIME,
	created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
)`,
	`CREATE TABLE IF NOT EXISTS audit_log (
	id         INTEGER PRIMARY KEY,
	table_name TEXT    NOT NULL,
	record_id  INTEGER NOT NULL,
	action     TEXT    NOT NULL,
	old_data   TEXT,
	new_data   TEXT,
	operator   TEXT,
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
)`,
	`CREATE INDEX IF NOT EXISTS idx_products_status ON products(status)`,
	`CREATE INDEX IF NOT EXISTS idx_parts_status ON parts(status)`,
	`CREATE INDEX IF NOT EXISTS idx_parts_type ON parts(part_type)`,
	`CREATE INDEX IF NOT EXISTS idx_bom_product ON bom_items(product_id)`,
	`CREATE INDEX IF NOT EXISTS idx_bom_part ON bom_items(part_id)`,
	`CREATE INDEX IF NOT EXISTS idx_batches_product ON product_batches(product_id)`,
	`CREATE INDEX IF NOT EXISTS idx_batches_status ON product_batches(status)`,
	`CREATE INDEX IF NOT EXISTS idx_trace_batch ON batch_trace(batch_id)`,
	`CREATE INDEX IF NOT EXISTS idx_trace_part ON batch_trace(part_id)`,
	`CREATE INDEX IF NOT EXISTS idx_table_record ON audit_log(table_name, record_id)`,
	`CREATE INDEX IF NOT EXISTS idx_created ON audit_log(created_at)`,
}

// sqliteBaselineVersions 是空库落地终态后写入 schema_migrations 的版本清单。
var sqliteBaselineVersions = []struct {
	Version int
	Name    string
}{
	{1, "base_schema"},
	{2, "parts_warn_qty"},
	{3, "bom_replaceable_use_mode"},
	{4, "products_drop_code_index"},
	{5, "batch_skip_parts"},
	{6, "parts_supplier"},
	{7, "batches_customer"},
	{8, "users"},
	{9, "batch_consumptions"},
	{10, "quantity_checks"},
	{11, "batch_consumption_checks"},
}

// CurrentSchemaVersion 与 MySQL 迁移表保持一致的最新基线版本号。
// v12+ 请走 steps（双端）；基线版本只在空库一次落地时使用。
const CurrentSchemaVersion = 11
