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
