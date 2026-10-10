/**
 * 模块权限档位，由后端 /api/me/permissions 下发。
 *
 * 前端不持有权限矩阵：矩阵只存在于 internal/auth 的 moduleAccess。
 * 「无」意味着整个入口不显示，而不是置灰——置灰会让仓管以为软件坏了
 * （看不到的用户管理入口正是这种误会的来源）。
 *
 * 这不是安全边界。每个请求仍由服务端 usecase.Allow 重算权限；
 * 前端隐藏只用来减少误操作与困惑。
 */
export type Access = 'read' | 'write' | 'none'

export type ModulePermissions = Record<string, Access>

export function canRead(modules: ModulePermissions | null, key: string): boolean {
  if (!modules) return false
  return modules[key] === 'read' || modules[key] === 'write'
}

export function canWrite(modules: ModulePermissions | null, key: string): boolean {
  if (!modules) return false
  return modules[key] === 'write'
}
