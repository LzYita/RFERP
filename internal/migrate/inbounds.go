package migrate

import (
	"time"

	"github.com/jmoiron/sqlx"
)

// part_inbounds 记录零件的每一笔入库：谁（哪笔批次号）、什么时候、进了多少。
//
// 库存数量本身仍以 parts.stock_qty 为准——生产完成扣料与盘点调整都会改动它
// 却不产生入库记录；这张表回答的是"现有库存是哪几笔入库攒出来的"。
// 批次号由应用生成（RK-YYYYMMDD-NNNN，NNNN 取行 id），生成依赖自增 id，
// 所以先插行（batch_no 为 NULL）再回填；两步同事务，读者永远看不到 NULL。
// 删除零件时入库记录随之级联消失：零件都没了，它的入库台账没有独立意义。
const partInboundsDDLMySQL = `CREATE TABLE IF NOT EXISTS part_inbounds (
	id         BIGINT AUTO_INCREMENT PRIMARY KEY,
	part_id    BIGINT NOT NULL,
	batch_no   VARCHAR(40) NULL,
	qty        DECIMAL(12,2) NOT NULL,
	note       VARCHAR(200) NULL COMMENT '备注，如来源说明',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (part_id) REFERENCES parts(id) ON DELETE CASCADE,
	UNIQUE KEY uk_part_inbound_no (batch_no),
	CHECK (qty > 0)
) COMMENT '零件入库批次-自动批次号'`

const partInboundsDDLite = `CREATE TABLE IF NOT EXISTS part_inbounds (
	id         INTEGER PRIMARY KEY,
	part_id    INTEGER NOT NULL REFERENCES parts(id) ON DELETE CASCADE,
	batch_no   TEXT NULL,
	qty        NUMERIC NOT NULL CHECK (qty > 0),
	note       TEXT NULL,
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
	UNIQUE (batch_no)
)`

// v16 落表之外还要做旧版适配：升级前零件的库存是新增/编辑表单里直接填的，
// 没有任何入库痕迹。把每个零件的现有库存整体记成一笔"历史库存初始化"，
// 让详情页对老数据也有账可查。此后零件数量只能经入库/盘点变动。
//
// 幂等性：INSERT 带 NOT EXISTS 守卫，回填只认 batch_no IS NULL——
// 迁移中途失败重跑（步骤未记录版本）时既不会重复播种，也不会留下空批次号。
func applyPartInboundsMySQL(db *sqlx.DB) error {
	if _, err := db.Exec(partInboundsDDLMySQL); err != nil {
		return err
	}
	return seedPartInbounds(db, "CONCAT('RK-', ?, '-', LPAD(id, 4, '0'))")
}

func applyPartInboundsSQLite(db *sqlx.DB) error {
	if _, err := db.Exec(partInboundsDDLite); err != nil {
		return err
	}
	return seedPartInbounds(db, "'RK-' || ? || '-' || printf('%04d', id)")
}

func seedPartInbounds(db *sqlx.DB, batchNoExpr string) error {
	if _, err := db.Exec(`
		INSERT INTO part_inbounds (part_id, qty, note)
		SELECT p.id, p.stock_qty, '历史库存初始化' FROM parts p
		WHERE p.stock_qty > 0
		  AND NOT EXISTS (SELECT 1 FROM part_inbounds i WHERE i.part_id = p.id)`); err != nil {
		return err
	}
	_, err := db.Exec(`UPDATE part_inbounds SET batch_no = `+batchNoExpr+` WHERE batch_no IS NULL`,
		time.Now().Format("20060102"))
	return err
}
