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
	{Name: "Catalog.UpdateBOMItem", Module: auth.ModuleBOM, Access: auth.AccessWrite},
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
	{Name: "Production.ListTempParts", Module: auth.ModuleBatch, Access: auth.AccessRead},
	{Name: "Production.AddTempPart", Module: auth.ModuleBatch, Access: auth.AccessWrite},
	{Name: "Production.RemoveTempPart", Module: auth.ModuleBatch, Access: auth.AccessWrite},

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

	// 原先这里有三条 System.*（CheckUpdate / DownloadUpdate / ApplyUpdate），
	// 挂在 ModuleSystem 下、写权限只给管理员。2026-10-15 全部删除：软件更新
	// 对齐 Fyne 旧版，**任何登录角色都能更新**，权限表里不该再留一个只对管理员
	// 开放、却与产品行为矛盾的登记项。三个端点现在的接线是：
	//
	//   GET  /api/update/check    不挂 auth —— 登录页要在登录前提示可更新
	//   POST /api/update/download 挂 s.auth(h, "") —— 登录即可
	//   POST /api/update/apply    挂 s.auth(h, "") —— 登录即可，且必须 confirm:true
	//
	// 空权限点的两条要在 internal/api/routes_permissions_test.go 的
	// routesWithoutOpPoint 里登记理由；check 不挂 auth 的理由登记在
	// routesWithoutAuth。安全边界靠签名与哈希，不靠角色，见 internal/api/update.go。

	// Backup / export / clear
	{Name: "Backup.DataDir", Module: auth.ModuleBackup, Access: auth.AccessRead},
	{Name: "Backup.SetDataDir", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.BackupDatabase", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.RestoreDatabase", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.ClearDatabase", Module: auth.ModuleBackup, Access: auth.AccessWrite},
	{Name: "Backup.ExportAuditLogCSV", Module: auth.ModuleBackup, Access: auth.AccessRead},
	{Name: "Backup.ExportAllDataCSV", Module: auth.ModuleBackup, Access: auth.AccessRead},

	// Identity
	//
	// UserCount 同样是登记性质的：`GET /api/users/count` 不挂鉴权，
	// 因为首启要据此决定是进"初始化管理员"还是"登录页"——有会话才能问的话
	// 就没有首启流程了。它只回一个数量，不泄露任何账号信息。
	{Name: "Identity.UserCount", Module: auth.ModuleUsers, Access: auth.AccessRead},
	{Name: "Identity.ListUsers", Module: auth.ModuleUsers, Access: auth.AccessRead},
	{Name: "Identity.CreateUser", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	{Name: "Identity.UpdateUser", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	{Name: "Identity.DeleteUser", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	{Name: "Identity.ResetPassword", Module: auth.ModuleUsers, Access: auth.AccessWrite},
	// 改自己的密码：**任何登录用户都可以**，路由因此用空权限点而不是这一行。
	// handler 内强制 in.ID == 会话用户 ID（api/admin.go），越权改他人会 403。
	// 记在这里是为了让"密码相关能力"在权限表里可检索，**不代表**它是管理员专属：
	// 若照这一行去收紧路由，普通用户就再也改不了自己的密码。
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
