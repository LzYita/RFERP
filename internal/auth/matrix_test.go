package auth

import "testing"

// 权限矩阵的逐项核对。
//
// 这个文件的用途不是"再抄一遍矩阵然后相等断言"——那样矩阵改了两边一起改，
// 测试永远绿。它锁的是**结构不变量**：模块清单是否完整、每个模块是否对四个
// 角色都有明确档位、以及几条经过决策的具体档位不被无声改掉。
//
// 背景：AllModules() 曾经是手写的字面量列表，与 moduleAccess 分开维护；
// 新增 ModuleSystem 时只加了 map、忘了列表，于是 /api/me/permissions 少返回
// 一个模块，前端永远看不到它。下面的 TestEveryModuleIsListed 就是为了挡住它。

// 模块清单必须与权限表完全一致：不多、不少、不重。
func TestEveryModuleIsListed(t *testing.T) {
	listed := AllModules()
	seen := map[string]bool{}
	for _, m := range listed {
		if seen[m] {
			t.Errorf("模块 %q 在 AllModules() 里重复出现", m)
		}
		seen[m] = true
		if _, ok := moduleAccess[m]; !ok {
			t.Errorf("AllModules() 列出了 %q，但它不在权限表里", m)
		}
	}
	for m := range moduleAccess {
		if !seen[m] {
			t.Errorf("权限表里有 %q，但 AllModules() 没有列出——前端将永远看不到它", m)
		}
	}
	if len(listed) != len(moduleAccess) {
		t.Errorf("AllModules() 有 %d 项，权限表有 %d 项", len(listed), len(moduleAccess))
	}
}

// 每个模块都必须对四个角色显式给出档位。
//
// 漏写的后果不是报错，而是 AccessFor 静默落回 AccessNone——看起来像"故意不给
// 权限"，实际是忘了写。评审时两者无法区分，所以在这里挡住。
func TestEveryModuleCoversEveryRole(t *testing.T) {
	for _, m := range AllModules() {
		grants := moduleAccess[m]
		for _, r := range Roles() {
			if _, ok := grants[r]; !ok {
				t.Errorf("模块 %q 缺少角色 %q 的档位（会静默按 none 处理）", m, r)
			}
		}
		if len(grants) != len(Roles()) {
			t.Errorf("模块 %q 有 %d 个档位，角色有 %d 个（多出来的键不会被使用）",
				m, len(grants), len(Roles()))
		}
	}
}

// 未知模块/未知角色必须落到 AccessNone，而不是 panic 或意外放行。
func TestUnknownModuleAndRoleFallBackToNone(t *testing.T) {
	if got := AccessFor(RoleAdmin, "no_such_module"); got != AccessNone {
		t.Errorf("未知模块 = %v, want AccessNone", got)
	}
	if got := AccessFor(Role("no_such_role"), ModuleParts); got != AccessNone {
		t.Errorf("未知角色 = %v, want AccessNone", got)
	}
}

// 决策过的档位逐项锁死。
//
// 这些值都有明确来历，改动等于改产品行为，必须显式改这份表：
//   - audit 对 viewer 是 none（2026-10-10 定：只读不得看操作记录，含操作人账号）
//   - system 只有 admin 是 write（替换可执行文件等于交出程序控制权）
//   - parts 是唯一仓管可写的目录模块（入库/盘点属仓管本职）
//   - batch 是唯一生产可写的模块（生产排产和投料）
//   - backup 对生产/只读是 none（整库恢复/清空是破坏性操作）
type grant struct {
	module string
	role   Role
	want   Access
}

func TestDecidedGrants(t *testing.T) {
	cases := []grant{
		// 全员可读
		{ModuleDashboard, RoleViewer, AccessRead},
		{ModuleStats, RoleProduction, AccessRead},

		// 目录：仓管可写，生产/只读只读
		{ModuleProducts, RoleWarehouse, AccessRead},
		{ModuleProducts, RoleViewer, AccessRead},
		{ModuleParts, RoleWarehouse, AccessWrite},
		{ModuleParts, RoleProduction, AccessRead},
		{ModuleParts, RoleViewer, AccessRead},
		{ModuleBOM, RoleWarehouse, AccessRead},
		{ModuleBOM, RoleProduction, AccessRead},
		{ModuleBOM, RoleViewer, AccessRead},
		{ModuleBOM, RoleAdmin, AccessWrite},

		// 生产
		{ModuleBatch, RoleProduction, AccessWrite},
		{ModuleBatch, RoleWarehouse, AccessRead},
		{ModuleBatch, RoleViewer, AccessRead},
		{ModuleBatch, RoleAdmin, AccessWrite},

		// 只读看不到操作记录
		{ModuleAudit, RoleViewer, AccessNone},
		{ModuleAudit, RoleWarehouse, AccessRead},
		{ModuleAudit, RoleProduction, AccessRead},
		{ModuleAudit, RoleAdmin, AccessRead},

		// 备份/导出：生产与只读无权限
		{ModuleBackup, RoleAdmin, AccessWrite},
		{ModuleBackup, RoleWarehouse, AccessRead},
		{ModuleBackup, RoleProduction, AccessNone},
		{ModuleBackup, RoleViewer, AccessNone},

		// 用户管理：仅管理员
		{ModuleUsers, RoleAdmin, AccessWrite},
		{ModuleUsers, RoleWarehouse, AccessNone},
		{ModuleUsers, RoleProduction, AccessNone},
		{ModuleUsers, RoleViewer, AccessNone},

		// 应用维护：仅管理员
		{ModuleSystem, RoleAdmin, AccessWrite},
		{ModuleSystem, RoleWarehouse, AccessNone},
		{ModuleSystem, RoleProduction, AccessNone},
		{ModuleSystem, RoleViewer, AccessNone},
	}
	for _, c := range cases {
		got := AccessFor(c.role, c.module)
		if got != c.want {
			t.Errorf("AccessFor(%s, %s) = %v, want %v", c.role, c.module, got, c.want)
		}
	}
}

// 写权限必须蕴含读权限，否则会出现"能改但看不到"的荒谬档位。
// （AccessNone < AccessRead < AccessWrite，所以只要断言不小于即可。）
func TestWriteImpliesRead(t *testing.T) {
	for _, m := range AllModules() {
		for _, r := range Roles() {
			a := AccessFor(r, m)
			if a != AccessWrite {
				continue
			}
			if a < AccessRead {
				t.Errorf("%s/%s 是 write 但不满足 read", r, m)
			}
		}
	}
}

// ModuleSystem 必须在清单里——这条单独列出，因为漏掉它正是当初的事故。
func TestModuleSystemIsExposed(t *testing.T) {
	found := false
	for _, m := range AllModules() {
		if m == ModuleSystem {
			found = true
		}
	}
	if !found {
		t.Fatal("ModuleSystem 不在 AllModules() 里，前端将无法据权限点门控更新入口")
	}
	if AccessFor(RoleAdmin, ModuleSystem) != AccessWrite {
		t.Error("管理员对 ModuleSystem 应是 write")
	}
}
