package usecase

import "app/internal/auth"

// UseCasePermission 将用例映射到 auth 模块权限（Phase B3）。
// UI 隐藏入口；HTTP API 必须在服务端按本表强制校验，不得只靠客户端。
type UseCasePermission struct {
	// Name 形如 "Inventory.StockIn"，供 API/审计引用。
	Name   string
	Module string
	Access auth.Access
}

// 读类用例统一 AccessRead；写类 AccessWrite。登录/初始管理员不在此表（独立流程）。
var permissions = []UseCasePermission{
	// Catalog / products
	{Name: "Catalog.GetProduct", Module: auth.ModuleProducts, Access: auth.AccessRead},
	{Name: "Catalog.ListProducts", Module: auth.ModuleProducts, Access: auth.AccessRead},
	{Name: "Catalog.CreateProduct", Module: auth.ModuleProducts, Access: auth.AccessWrite},
	{Name: "Catalog.UpdateProduct", Module: auth.ModuleProducts, Access: auth.AccessWrite},
	{Name: "Catalog.DeleteProduct", Module: auth.ModuleProducts, Access: auth.AccessWrite},

	// Catalog / parts
	{Name: "Catalog.GetPart", Module: auth.ModuleParts, Access: auth.AccessRead},
	{Name: "Catalog.ListParts", Module: auth.ModuleParts, Access: auth.AccessRead},
	{Name: "Catalog.CreatePart", Module: auth.ModuleParts, Access: auth.AccessWrite},
	{Name: "Catalog.UpdatePart", Module: auth.ModuleParts, Access: auth.AccessWrite},
	{Name: "Catalog.DeletePart", Module: auth.ModuleParts, Access: auth.AccessWrite},

	// Catalog / BOM
	{Name: "Catalog.GetBOMByProduct", Module: auth.ModuleBOM, Access: auth.AccessRead},
	{Name: "Catalog.ValidateBOM", Module: auth.ModuleBOM, Access: auth.AccessRead},
	{Name: "Catalog.AddBOMItem", Module: auth.ModuleBOM, Access: auth.AccessWrite},
	{Name: "Catalog.RemoveBOMItem", Module: auth.ModuleBOM, Access: auth.AccessWrite},

	// Inventory
	{Name: "Inventory.StockIn", Module: auth.ModuleParts, Access: auth.AccessWrite},
	{Name: "Inventory.AdjustStock", Module: auth.ModuleParts, Access: auth.AccessWrite},

	// Production
	{Name: "Production.ListBatches", Module: auth.ModuleBatch, Access: auth.AccessRead},
	{Name: "Production.GetSkippedParts", Module: auth.ModuleBatch, Access: auth.AccessRead},
	{Name: "Production.CreateBatch", Module: auth.ModuleBatch, Access: auth.AccessWrite},
	{Name: "Production.UpdateBatchStatus", Module: auth.ModuleBatch, Access: auth.AccessWrite},
	{Name: "Production.RevokeBatch", Module: auth.ModuleBatch, Access: auth.AccessWrite},
	{Name: "Production.AddSkipPart", Module: auth.ModuleBatch, Access: auth.AccessWrite},
	{Name: "Production.RemoveSkipPart", Module: auth.ModuleBatch, Access: auth.AccessWrite},

	// Trace
	{Name: "Trace.GetTraceByBatch", Module: auth.ModuleBatch, Access: auth.AccessRead},
	{Name: "Trace.TraceByProduct", Module: auth.ModuleBatch, Access: auth.AccessRead},
	{Name: "Trace.TraceByPart", Module: auth.ModuleBatch, Access: auth.AccessRead},
	{Name: "Trace.RecordTrace", Module: auth.ModuleBatch, Access: auth.AccessWrite},
	{Name: "Trace.RecordTraces", Module: auth.ModuleBatch, Access: auth.AccessWrite},

	// Audit
	{Name: "Audit.GetAuditLogs", Module: auth.ModuleAudit, Access: auth.AccessRead},
	{Name: "Audit.ListRecentAuditLogs", Module: auth.ModuleAudit, Access: auth.AccessRead},

	// Stats / dashboard 只读
	{Name: "Stats.GetStockStats", Module: auth.ModuleStats, Access: auth.AccessRead},

	// Backup / export / clear
	{Name: "Backup.DataDir", Module: auth.ModuleBackup, Access: auth.AccessRead},
	{Name: "Backup.SetDataDir", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.BackupDatabase", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.RestoreDatabase", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.ClearDatabase", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.ExportAuditLogCSV", Module: auth.ModuleBackup, Access: auth.AccessRead},
	{Name: "Backup.ExportAllDataCSV", Module: auth.ModuleBackup, Access: auth.AccessRead},

	// Identity
	{Name: "Identity.UserCount", Module: auth.ModuleUsers, Access: auth.AccessRead},
	{Name: "Identity.ListUsers", Module: auth.ModuleUsers, Access: auth.AccessRead},
	{Name: "Identity.CreateUser", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	{Name: "Identity.UpdateUser", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	{Name: "Identity.DeleteUser", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	{Name: "Identity.ResetPassword", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	// 改自己的密码：登录用户即可，模块记为 users/write（与现 UI 管理入口一致）。
	{Name: "Identity.ChangePassword", Module: auth.ModuleUsers, Access: auth.AccessWrite},
}

var permissionByName = func() map[string]UseCasePermission {
	m := make(map[string]UseCasePermission, len(permissions))
	for _, p := range permissions {
		m[p.Name] = p
	}
	return m
}()

// PermissionFor 返回用例权限点；未知用例返回 false。
func PermissionFor(name string) (UseCasePermission, bool) {
	p, ok := permissionByName[name]
	return p, ok
}

// Allow 判断角色是否满足用例所需权限。
func Allow(role auth.Role, name string) bool {
	p, ok := PermissionFor(name)
	if !ok {
		return false
	}
	return auth.AccessFor(role, p.Module) >= p.Access
}

// AllPermissions 返回完整权限点表（供文档/代码生成/测试对照）。
func AllPermissions() []UseCasePermission {
	out := make([]UseCasePermission, len(permissions))
	copy(out, permissions)
	return out
}
