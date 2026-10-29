package migrate

import "github.com/jmoiron/sqlx"

// v18 让投料记录能说明「这一批用量的零件批次来源及其先后顺序」。
//
// 之前 batch_trace.part_batch_no 是一串自由文本，和 part_inbounds 里的
// 真实批次号没有关联：既算不出这批料从哪个批次出、也没法按用户排的顺序
// 扣批次。v18 新增 batch_trace_allocs，一个零件可以挂多条分配
// （inbound_id + qty + position），完成生产时按 position 先后扣。
//
// 同 (batch_id, part_id) 的分配随投料记录一起整体替换：记录投料从
// 「每次追加」改成「按零件覆盖」，历史行不自动迁移——旧记录没有
// inbound_id，强行猜一个批次只会造出假账，保留 part_batch_no 原文即可。
const traceAllocsDDLMySQL = `CREATE TABLE IF NOT EXISTS batch_trace_allocs (
	id         BIGINT AUTO_INCREMENT PRIMARY KEY,
	batch_id   BIGINT NOT NULL,
	part_id    BIGINT NOT NULL,
	inbound_id BIGINT NOT NULL,
	qty        DECIMAL(12,2) NOT NULL,
	position   INT NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (batch_id) REFERENCES product_batches(id) ON DELETE CASCADE,
	FOREIGN KEY (inbound_id) REFERENCES part_inbounds(id) ON DELETE CASCADE,
	INDEX idx_alloc_batch_part (batch_id, part_id),
	INDEX idx_alloc_inbound (inbound_id),
	CHECK (qty > 0)
) COMMENT '投料批次分配-按顺序扣料依据'`

const traceAllocsDDLite = `CREATE TABLE IF NOT EXISTS batch_trace_allocs (
	id         INTEGER PRIMARY KEY,
	batch_id   INTEGER NOT NULL REFERENCES product_batches(id) ON DELETE CASCADE,
	part_id    INTEGER NOT NULL,
	inbound_id INTEGER NOT NULL REFERENCES part_inbounds(id) ON DELETE CASCADE,
	qty        NUMERIC NOT NULL CHECK (qty > 0),
	position   INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
)`

func applyTraceAllocsMySQL(db *sqlx.DB) error {
	_, err := db.Exec(traceAllocsDDLMySQL)
	return err
}

func applyTraceAllocsSQLite(db *sqlx.DB) error {
	if _, err := db.Exec(traceAllocsDDLite); err != nil {
		return err
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_alloc_batch_part ON batch_trace_allocs (batch_id, part_id)`,
		`CREATE INDEX IF NOT EXISTS idx_alloc_inbound ON batch_trace_allocs (inbound_id)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
