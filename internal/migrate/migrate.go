package migrate

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"

	"app/internal/dbbackup"
)

type Options struct {
	DSN           string
	MysqldumpPath string
	BackupDir     string
}

type Result struct {
	FromVersion int
	ToVersion   int
	Applied     []int
	BackupPath  string
}

type migration struct {
	Version int
	Name    string
	Apply   func(*sqlx.DB) error
}

var migrations = []migration{
	{1, "base_schema", applyBaseSchema},
	{2, "parts_warn_qty", applyPartsWarnQty},
	{3, "bom_replaceable_use_mode", applyBOMReplaceableUseMode},
	{4, "products_drop_code_index", applyProductsDropCodeIndex},
	{5, "batch_skip_parts", applyBatchSkipParts},
	{6, "parts_supplier", applyPartsSupplier},
	{7, "batches_customer", applyBatchesCustomer},
	{8, "users", applyUsers},
	{9, "batch_consumptions", applyBatchConsumptions},
	{10, "quantity_checks", applyQuantityChecks},
	{11, "batch_consumption_checks", applyBatchConsumptionChecks},
}

func Run(db *sqlx.DB, opts Options) (Result, error) {
	var res Result
	if err := ensureMigrationsTable(db); err != nil {
		return res, err
	}
	cur, err := currentVersion(db)
	if err != nil {
		return res, err
	}
	res.FromVersion = cur

	var pending []migration
	for _, m := range migrations {
		if m.Version > cur {
			pending = append(pending, m)
		}
	}
		if len(pending) == 0 {
		res.ToVersion = cur
		if err := applyStepMigrations(db, cur, &res); err != nil {
			return res, err
		}
		return res, nil
	}

	empty, err := databaseEmpty(db)
	if err != nil {
		log.Printf("migrate: check empty failed: %v", err)
		empty = true
	}
	if !empty && opts.BackupDir != "" {
		path, err := dbbackup.Backup(opts.DSN, opts.MysqldumpPath, opts.BackupDir)
		if err != nil {
			log.Printf("migrate: pre-upgrade backup failed: %v", err)
		} else {
			res.BackupPath = path
			log.Printf("migrate: pre-upgrade backup saved: %s", path)
		}
	}

	for _, m := range pending {
		log.Printf("migrate: applying v%d %s", m.Version, m.Name)
		if err := m.Apply(db); err != nil {
			return res, fmt.Errorf("migration v%d (%s) failed: %w", m.Version, m.Name, err)
		}
		if err := recordVersion(db, m.Version, m.Name); err != nil {
			return res, fmt.Errorf("record v%d: %w", m.Version, err)
		}
		res.Applied = append(res.Applied, m.Version)
	}
		res.ToVersion = pending[len(pending)-1].Version
	// v12+ portable steps (dual MySQL/SQLite). Snapshot before these when DB is non-empty.
	if err := applyStepMigrations(db, cur, &res); err != nil {
		return res, err
	}
	return res, nil
}

func ensureMigrationsTable(db *sqlx.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INT          NOT NULL PRIMARY KEY,
		name       VARCHAR(128) NOT NULL,
		applied_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
	) COMMENT '数据库迁移版本记录'`)
	return err
}

func currentVersion(db *sqlx.DB) (int, error) {
	var v sql.NullInt64
	if err := db.Get(&v, `SELECT MAX(version) FROM schema_migrations`); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

func recordVersion(db *sqlx.DB, version int, name string) error {
	_, err := db.Exec(`INSERT IGNORE INTO schema_migrations (version, name) VALUES (?, ?)`, version, name)
	return err
}

func databaseEmpty(db *sqlx.DB) (bool, error) {
	var n int
	err := db.Get(&n, `SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME <> 'schema_migrations'`)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

func columnExists(db *sqlx.DB, table, column string) (bool, error) {
	var n int
	err := db.Get(&n, `SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`, table, column)
	return n > 0, err
}

func constraintExists(db *sqlx.DB, table, constraint string) (bool, error) {
	var n int
	err := db.Get(&n, `SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS
		WHERE CONSTRAINT_SCHEMA = DATABASE() AND TABLE_NAME = ? AND CONSTRAINT_NAME = ?
		  AND CONSTRAINT_TYPE = 'CHECK'`, table, constraint)
	return n > 0, err
}

func indexExists(db *sqlx.DB, table, index string) (bool, error) {
	var n int
	err := db.Get(&n, `SELECT COUNT(*) FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?`, table, index)
	return n > 0, err
}

func applyBaseSchema(db *sqlx.DB) error {
	for _, stmt := range baseSchema {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	indexes := []struct {
		name, table, cols string
	}{
		{"idx_products_status", "products", "status"},
		{"idx_parts_status", "parts", "status"},
		{"idx_parts_type", "parts", "part_type"},
		{"idx_bom_product", "bom_items", "product_id"},
		{"idx_bom_part", "bom_items", "part_id"},
		{"idx_batches_product", "product_batches", "product_id"},
		{"idx_batches_status", "product_batches", "status"},
		{"idx_trace_batch", "batch_trace", "batch_id"},
		{"idx_trace_part", "batch_trace", "part_id"},
	}
	for _, ix := range indexes {
		ok, err := indexExists(db, ix.table, ix.name)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("CREATE INDEX %s ON %s(%s)", ix.name, ix.table, ix.cols)); err != nil {
			return err
		}
	}
	return nil
}

func applyPartsWarnQty(db *sqlx.DB) error {
	ok, err := columnExists(db, "parts", "warn_qty")
	if err != nil || ok {
		return err
	}
	_, err = db.Exec(`ALTER TABLE parts ADD COLUMN warn_qty DECIMAL(12,2) NOT NULL DEFAULT 0 COMMENT '库存预警数量'`)
	return err
}

func applyBOMReplaceableUseMode(db *sqlx.DB) error {
	ok, err := columnExists(db, "bom_items", "replaceable")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := db.Exec(`ALTER TABLE bom_items ADD COLUMN replaceable TINYINT NOT NULL DEFAULT 0 COMMENT '可替换:1是 0否'`); err != nil {
			return err
		}
	}
	ok, err = columnExists(db, "bom_items", "use_mode")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := db.Exec(`ALTER TABLE bom_items ADD COLUMN use_mode TINYINT NOT NULL DEFAULT 0 COMMENT '用量模式:0=每台用N个 1=每M台用1个'`); err != nil {
			return err
		}
	}
	return nil
}

func applyProductsDropCodeIndex(db *sqlx.DB) error {
	ok, err := indexExists(db, "products", "code")
	if err != nil || !ok {
		return err
	}
	_, err = db.Exec(`ALTER TABLE products DROP INDEX code`)
	return err
}

func applyBatchSkipParts(db *sqlx.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS batch_skip_parts (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		batch_id BIGINT NOT NULL,
		part_id BIGINT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (batch_id) REFERENCES product_batches(id) ON DELETE CASCADE,
		FOREIGN KEY (part_id) REFERENCES parts(id),
		UNIQUE KEY uk_batch_part (batch_id, part_id)
	) COMMENT '批次跳过零件-不消耗库存'`)
	return err
}

func applyPartsSupplier(db *sqlx.DB) error {
	ok, err := columnExists(db, "parts", "supplier")
	if err != nil || ok {
		return err
	}
	_, err = db.Exec(`ALTER TABLE parts ADD COLUMN supplier VARCHAR(200) COMMENT '供应商'`)
	return err
}

func applyBatchesCustomer(db *sqlx.DB) error {
	ok, err := columnExists(db, "product_batches", "customer")
	if err != nil || ok {
		return err
	}
	_, err = db.Exec(`ALTER TABLE product_batches ADD COLUMN customer VARCHAR(200) COMMENT '客户'`)
	return err
}

func applyUsers(db *sqlx.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id            BIGINT AUTO_INCREMENT PRIMARY KEY,
		username      VARCHAR(64)  NOT NULL UNIQUE COMMENT '登录名',
		password_hash VARCHAR(255) NOT NULL COMMENT '密码哈希',
		display_name  VARCHAR(100)          COMMENT '显示名',
		role          VARCHAR(20)  NOT NULL DEFAULT 'viewer' COMMENT 'admin/warehouse/production/viewer',
		status        TINYINT      NOT NULL DEFAULT 1 COMMENT '1启用 0停用',
		last_login_at DATETIME     NULL,
		created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	) COMMENT '用户账号'`)
	return err
}

func applyBatchConsumptions(db *sqlx.DB) error {
	ok, err := columnExists(db, "product_batches", "consumption_recorded")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := db.Exec(`ALTER TABLE product_batches ADD COLUMN consumption_recorded TINYINT NOT NULL DEFAULT 0 COMMENT '是否已冻结批次实际库存消耗'`); err != nil {
			return err
		}
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS batch_consumptions (
		id           BIGINT AUTO_INCREMENT PRIMARY KEY,
		batch_id     BIGINT NOT NULL,
		part_id      BIGINT NOT NULL,
		consumed_qty DECIMAL(12,2) NOT NULL,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (batch_id) REFERENCES product_batches(id) ON DELETE CASCADE,
		FOREIGN KEY (part_id) REFERENCES parts(id),
		UNIQUE KEY uk_batch_consumption (batch_id, part_id)
	) COMMENT '批次实际消耗记录-完成时冻结'`)
	if err != nil {
		return err
	}

	// Older completed batches only have STOCK_DEDUCT audit rows. Recover the
	// actual quantity removed (old_stock-new_stock), not the requested quantity,
	// so they can still be revoked without inventing stock.
	_, err = db.Exec(`
		INSERT INTO batch_consumptions (batch_id, part_id, consumed_qty)
		SELECT
			CAST(JSON_UNQUOTE(JSON_EXTRACT(a.new_data, '$.batch_id')) AS UNSIGNED),
			a.record_id,
			SUM(GREATEST(
				CAST(JSON_UNQUOTE(JSON_EXTRACT(a.new_data, '$.old_stock')) AS DECIMAL(12,2)) -
				CAST(JSON_UNQUOTE(JSON_EXTRACT(a.new_data, '$.new_stock')) AS DECIMAL(12,2)),
				0
			))
		FROM audit_log a
		JOIN product_batches b ON b.id = CAST(JSON_UNQUOTE(JSON_EXTRACT(a.new_data, '$.batch_id')) AS UNSIGNED)
		JOIN parts p ON p.id = a.record_id
		WHERE a.table_name = 'parts'
		  AND a.action = 'STOCK_DEDUCT'
		  AND b.status = 2
		  AND JSON_EXTRACT(a.new_data, '$.batch_id') IS NOT NULL
		  AND JSON_EXTRACT(a.new_data, '$.old_stock') IS NOT NULL
		  AND JSON_EXTRACT(a.new_data, '$.new_stock') IS NOT NULL
		GROUP BY
			CAST(JSON_UNQUOTE(JSON_EXTRACT(a.new_data, '$.batch_id')) AS UNSIGNED),
			a.record_id
		ON DUPLICATE KEY UPDATE consumed_qty = VALUES(consumed_qty)`)
	if err != nil {
		return err
	}

	// Mark a batch recorded only when every non-skipped BOM part has a frozen
	// consumption row. Partial historical recovery must stay unsafe to revoke.
	_, err = db.Exec(`
		UPDATE product_batches b
		SET consumption_recorded = 1
		WHERE b.status = 2
		  AND NOT EXISTS (
			SELECT 1
			FROM bom_items bi
			WHERE bi.product_id = b.product_id
			  AND NOT EXISTS (
				SELECT 1 FROM batch_skip_parts sk
				WHERE sk.batch_id = b.id AND sk.part_id = bi.part_id
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM batch_consumptions c
				WHERE c.batch_id = b.id AND c.part_id = bi.part_id
			  )
		  )`)
	return err
}

func applyQuantityChecks(db *sqlx.DB) error {
	checks := []struct {
		table, name, expression string
	}{
		{"parts", "ck_parts_stock_nonnegative", "stock_qty >= 0"},
		{"parts", "ck_parts_warn_nonnegative", "warn_qty >= 0"},
		{"bom_items", "ck_bom_quantity_positive", "quantity > 0"},
		{"bom_items", "ck_bom_loss_rate_range", "loss_rate >= 0 AND loss_rate <= 100"},
		{"product_batches", "ck_batches_plan_positive", "plan_qty > 0"},
		{"batch_trace", "ck_trace_used_positive", "used_qty > 0"},
	}
	for _, check := range checks {
		exists, err := constraintExists(db, check.table, check.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE `%s` ADD CONSTRAINT `%s` CHECK (%s)", check.table, check.name, check.expression)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("add %s: %w", check.name, err)
		}
	}
	return nil
}

func applyBatchConsumptionChecks(db *sqlx.DB) error {
	const table = "batch_consumptions"
	const name = "ck_batch_consumptions_nonnegative"
	exists, err := constraintExists(db, table, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.Exec("ALTER TABLE `batch_consumptions` ADD CONSTRAINT `ck_batch_consumptions_nonnegative` CHECK (consumed_qty >= 0)")
	if err != nil {
		return fmt.Errorf("add %s: %w", name, err)
	}
	return nil
}

var baseSchema = []string{
	`CREATE TABLE IF NOT EXISTS products (
		id          BIGINT AUTO_INCREMENT PRIMARY KEY,
		code        VARCHAR(50)  NOT NULL UNIQUE COMMENT '产品编码',
		name        VARCHAR(200) NOT NULL COMMENT '产品名称',
		spec        VARCHAR(500)          COMMENT '规格型号',
		unit        VARCHAR(20)  NOT NULL DEFAULT '个' COMMENT '单位',
		status      TINYINT      NOT NULL DEFAULT 1  COMMENT '状态:1启用 0停用',
		version     INT          NOT NULL DEFAULT 1  COMMENT '乐观锁版本号',
		created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		operator    VARCHAR(50)          COMMENT '最后操作人'
	) COMMENT '产品档案'`,
	`CREATE TABLE IF NOT EXISTS parts (
		id          BIGINT AUTO_INCREMENT PRIMARY KEY,
		code        VARCHAR(50)  NOT NULL UNIQUE COMMENT '零件编码',
		name        VARCHAR(200) NOT NULL COMMENT '零件名称',
		spec        VARCHAR(500)          COMMENT '规格型号',
		unit        VARCHAR(20)  NOT NULL DEFAULT '个' COMMENT '单位',
		part_type   VARCHAR(50)          COMMENT '零件分类(电子/五金/塑料/包装/其他)',
		stock_qty   DECIMAL(12,2) NOT NULL DEFAULT 0 COMMENT '当前库存数量',
		status      TINYINT      NOT NULL DEFAULT 1  COMMENT '状态:1启用 0停用',
		version     INT          NOT NULL DEFAULT 1,
		created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		operator    VARCHAR(50),
		supplier    VARCHAR(200)          COMMENT '供应商'
	) COMMENT '零件/物料档案'`,
	`CREATE TABLE IF NOT EXISTS bom_items (
		id          BIGINT AUTO_INCREMENT PRIMARY KEY,
		product_id  BIGINT       NOT NULL COMMENT '产品ID',
		part_id     BIGINT       NOT NULL COMMENT '零件ID',
		quantity    DECIMAL(10,2) NOT NULL COMMENT '单台用量',
		loss_rate   DECIMAL(5,2) NOT NULL DEFAULT 0 COMMENT '损耗率(%)',
		remark      VARCHAR(500)          COMMENT '备注',
		version     INT          NOT NULL DEFAULT 1,
		created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		operator    VARCHAR(50),
		FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE,
		FOREIGN KEY (part_id)    REFERENCES parts(id),
		UNIQUE KEY uk_product_part (product_id, part_id)
	) COMMENT 'BOM明细-产品零件对应关系'`,
	`CREATE TABLE IF NOT EXISTS product_batches (
		id              BIGINT AUTO_INCREMENT PRIMARY KEY,
		batch_no        VARCHAR(100) NOT NULL UNIQUE COMMENT '生产批次号',
		product_id      BIGINT       NOT NULL COMMENT '产品ID',
		plan_qty        INT          NOT NULL COMMENT '计划数量',
		produced_qty    INT          NOT NULL DEFAULT 0 COMMENT '已完成数量',
		status          TINYINT      NOT NULL DEFAULT 0 COMMENT '状态:0待生产 1生产中 2已完成 3已暂停',
		version         INT          NOT NULL DEFAULT 1,
		created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		operator        VARCHAR(50),
		customer        VARCHAR(200)          COMMENT '客户',
		consumption_recorded TINYINT NOT NULL DEFAULT 0 COMMENT '是否已冻结批次实际库存消耗',
		FOREIGN KEY (product_id) REFERENCES products(id)
	) COMMENT '生产批次'`,
	`CREATE TABLE IF NOT EXISTS batch_trace (
		id              BIGINT AUTO_INCREMENT PRIMARY KEY,
		batch_id        BIGINT       NOT NULL COMMENT '产品批次ID',
		part_id         BIGINT       NOT NULL COMMENT '零件ID',
		part_batch_no   VARCHAR(100)          COMMENT '零件批次号/炉号',
		used_qty        DECIMAL(10,2) NOT NULL COMMENT '使用数量',
		supplier        VARCHAR(200)          COMMENT '供应商名称',
		created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		operator        VARCHAR(50),
		FOREIGN KEY (batch_id) REFERENCES product_batches(id),
		FOREIGN KEY (part_id)  REFERENCES parts(id)
	) COMMENT '批次追溯记录'`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id          BIGINT AUTO_INCREMENT PRIMARY KEY,
		table_name  VARCHAR(100) NOT NULL COMMENT '操作表名',
		record_id   BIGINT       NOT NULL COMMENT '记录ID',
		action      VARCHAR(20)  NOT NULL COMMENT 'INSERT/UPDATE/DELETE',
		old_data    JSON                  COMMENT '变更前数据',
		new_data    JSON                  COMMENT '变更后数据',
		operator    VARCHAR(50)           COMMENT '操作人',
		created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		INDEX idx_table_record (table_name, record_id),
		INDEX idx_created (created_at)
	) COMMENT '审计日志-记录所有数据变更'`,
}

// applyStepMigrations applies v12+ dual-end steps on MySQL.
func applyStepMigrations(db *sqlx.DB, from int, res *Result) error {
	for _, s := range steps {
		if s.Version <= from || s.MySQL == nil {
			continue
		}
		// Also skip if already recorded (idempotent re-run).
		var exists int
		_ = db.Get(&exists, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, s.Version)
		if exists > 0 {
			continue
		}
		log.Printf("migrate: applying v%d %s", s.Version, s.Name)
		if err := s.MySQL(db); err != nil {
			return fmt.Errorf("migration v%d (%s) failed: %w", s.Version, s.Name, err)
		}
		if err := recordVersion(db, s.Version, s.Name); err != nil {
			return fmt.Errorf("record v%d: %w", s.Version, err)
		}
		res.Applied = append(res.Applied, s.Version)
		res.ToVersion = s.Version
	}
	return nil
}

