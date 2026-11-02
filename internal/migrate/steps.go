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
// 曾计划用 v12 products_code_unique 恢复 MySQL products.code 唯一约束，
// 但真实数据证明产品编码**不唯一**：编码是产品系列号，同系列多颜色/规格共用
// 同一编码（同一编码对应多行产品）。加唯一索引会以
// ERROR 1062 (Duplicate entry) 失败，而迁移失败会直接阻断应用启动。
// 因此撤销该计划：products.code 保持「允许重复」，与 v4 之后的 MySQL 行为一致。
var steps = []Step{
	{
		Version: 13,
		Name:    "db_identity",
		MySQL:   applyDBIdentityMySQL,
		SQLite:  applyDBIdentitySQLite,
	},
	{
		Version: 14,
		Name:    "batch_temp_parts",
		MySQL:   applyBatchTempPartsMySQL,
		SQLite:  applyBatchTempPartsSQLite,
	},
	{
		Version: 15,
		Name:    "batch_temp_parts_derivation",
		MySQL:   applyTempPartsDerivationMySQL,
		SQLite:  applyTempPartsDerivationSQLite,
	},
	{
		Version: 16,
		Name:    "part_inbounds",
		MySQL:   applyPartInboundsMySQL,
		SQLite:  applyPartInboundsSQLite,
	},
	{
		Version: 17,
		Name:    "part_inbound_remaining",
		MySQL:   applyPartInboundRemainingMySQL,
		SQLite:  applyPartInboundRemainingSQLite,
	},
	{
		Version: 18,
		Name:    "batch_trace_allocs",
		MySQL:   applyTraceAllocsMySQL,
		SQLite:  applyTraceAllocsSQLite,
	},
	{
		// 局域网版：设备登记（授权按客户端设备数计，主机豁免）。
		Version: 19,
		Name:    "devices",
		MySQL:   applyDevicesMySQL,
		SQLite:  applyDevicesSQLite,
	},
	{
		// 局域网版：授权单行表 + 安全事件流水。两者同一版落地——
		// 授权导入与设备登记都要往 security_events 写留痕，缺表会让
		// 授权导入成功而审计失败，属于半成品状态。
		Version: 20,
		Name:    "license_and_security_events",
		MySQL:   applyLicenseAndEventsMySQL,
		SQLite:  applyLicenseAndEventsSQLite,
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
