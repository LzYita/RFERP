package service

import "app/internal/repository"

// 破坏性操作（整库恢复 / 清空数据）共用的表清单。
//
// 单一事实来源：「清空数据」与「整库恢复」必须删同一批表，
// 否则两处清单各自演化，会出现「清库清干净了、恢复却漏删某表」的错配。

// businessTables 会被两种操作清空，顺序为先子表后父表。
// 均为 DELETE（保留表结构），不含任何方言。
var businessTables = []string{
	"batch_skip_parts",
	"batch_trace",
	"batch_consumptions",
	"bom_items",
	"product_batches",
	"parts",
	"products",
	"audit_log",
}

// preservedTables 是破坏性操作中必须保留的表。
//
// 之所以保留三类数据：
//
//   - users：账号。按产品决定，账号不随业务数据一起回滚——
//     否则恢复一份旧备份后可能一个可用账号都不剩，
//     会出现「数据齐全但进不去」的死局。
//   - schema_migrations：现场结构版本。整库恢复只回灌数据、
//     不执行备份里的 DDL，所以表结构仍是当前版本，
//     迁移版本必须与现场结构一致，不能被备份里的旧值覆盖。
//   - db_identity：数据库的永久身份（v13 引入，写入后永不改动）。
//     它用于快照归属校验与服务器接入预检；一旦被回退，
//     这份库会被判定成「另一个库」。
//
// 该清单同时用于「跳过备份中对这些表的 INSERT」与 UI 文案，
// 避免文案与实现再次脱节。
var preservedTables = []string{
	"users",
	"schema_migrations",
	"db_identity",
}

// isPreservedTable 判断表是否属于必须保留的系统表。
func isPreservedTable(table string) bool {
	for _, t := range preservedTables {
		if t == table {
			return true
		}
	}
	return false
}

// deleteBusinessTables 逐表清空业务数据（保留表结构）。
func deleteBusinessTables(tx repository.TxOps) error {
	for _, table := range businessTables {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return nil
}
