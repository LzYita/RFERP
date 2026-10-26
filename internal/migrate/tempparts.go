package migrate

import "github.com/jmoiron/sqlx"

// batch_temp_parts 记录批次上的临时零件：不在产品 BOM 里、但这一批实际要用。
//
// 与 batch_skip_parts 同构（批次维度、批次删除级联、(batch_id, part_id) 唯一），
// 多一列 qty：临时零件加了就是要在完成时扣库存的，没有数量就没有扣减依据。
// qty 是本批总量；完成时整笔扣，撤销时按 batch_consumptions 冻结记录回退。
const tempPartsDDLMySQL = `CREATE TABLE IF NOT EXISTS batch_temp_parts (
	id         BIGINT AUTO_INCREMENT PRIMARY KEY,
	batch_id   BIGINT NOT NULL,
	part_id    BIGINT NOT NULL,
	qty        DECIMAL(12,2) NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (batch_id) REFERENCES product_batches(id) ON DELETE CASCADE,
	FOREIGN KEY (part_id) REFERENCES parts(id),
	UNIQUE KEY uk_batch_temp_part (batch_id, part_id),
	CHECK (qty > 0)
) COMMENT '批次临时零件-完成时扣库存'`

const tempPartsDDLite = `CREATE TABLE IF NOT EXISTS batch_temp_parts (
	id         INTEGER PRIMARY KEY,
	batch_id   INTEGER NOT NULL REFERENCES product_batches(id) ON DELETE CASCADE,
	part_id    INTEGER NOT NULL REFERENCES parts(id),
	qty        NUMERIC NOT NULL CHECK (qty > 0),
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	UNIQUE (batch_id, part_id)
)`

func applyBatchTempPartsMySQL(db *sqlx.DB) error {
	_, err := db.Exec(tempPartsDDLMySQL)
	return err
}

func applyBatchTempPartsSQLite(db *sqlx.DB) error {
	_, err := db.Exec(tempPartsDDLite)
	return err
}

// v15 给 batch_temp_parts 补"推导字段"：添加表单按 BOM 行的方式给
// 使用方式/单位用量/损耗率并自动算总量，这三列记录推导依据；用户手填
// 总量时三列为 NULL（总量 qty 永远是扣减依据，推导字段只是给人看的）。
// v14 已在部分库上落过（列不能 IF NOT EXISTS），所以用 v15 而不是改 v14。
const tempPartsDerivationMySQL = `ALTER TABLE batch_temp_parts
	ADD COLUMN use_mode SMALLINT NULL COMMENT '使用方式0每台N个/1每M台1个',
	ADD COLUMN quantity DECIMAL(12,2) NULL COMMENT '单位用量',
	ADD COLUMN loss_rate DECIMAL(5,2) NULL COMMENT '损耗率%'`

func applyTempPartsDerivationMySQL(db *sqlx.DB) error {
	for _, col := range []string{"use_mode", "quantity", "loss_rate"} {
		ok, err := columnExists(db, "batch_temp_parts", col)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if _, err := db.Exec(tempPartsDerivationMySQL); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func applyTempPartsDerivationSQLite(db *sqlx.DB) error {
	rows, err := db.Queryx(`PRAGMA table_info(batch_temp_parts)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dft any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dft, &pk); err != nil {
			return err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if have["use_mode"] {
		return nil
	}
	// SQLite 的 ALTER TABLE 一次只能加一列，必须拆三条。
	for _, stmt := range []string{
		`ALTER TABLE batch_temp_parts ADD COLUMN use_mode SMALLINT NULL`,
		`ALTER TABLE batch_temp_parts ADD COLUMN quantity NUMERIC NULL`,
		`ALTER TABLE batch_temp_parts ADD COLUMN loss_rate NUMERIC NULL`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
