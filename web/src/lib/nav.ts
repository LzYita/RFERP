/**
 * 导航定义。
 *
 * module 与 label 必须与 internal/ui/app.go 的 navItems 逐字一致
 * （app.go:196-204），顺序也一致。两者是同一个信息在两端的表现：
 * Go 侧是 Fyne 侧栏，前端是 Web 侧栏。改动必须同步。
 *
 * 权限不在这里判定：可见性由后端 /api/me/permissions 下发的模块档位决定
 * （见 lib/permissions.ts），前端不维护第二份权限矩阵。
 */
export type ModuleKey =
  | 'dashboard'
  | 'stats'
  | 'products'
  | 'parts'
  | 'bom'
  | 'batch'
  | 'audit'
  | 'backup'
  | 'users'

export interface NavEntry {
  key: ModuleKey
  /** 与 app.go 里的中文标签逐字相同 */
  label: string
  /** 路由 path */
  path: string
}

export const NAV: NavEntry[] = [
  { key: 'dashboard', label: '工作台', path: '/dashboard' },
  { key: 'stats', label: '统计分析', path: '/stats' },
  { key: 'products', label: '产品管理', path: '/products' },
  { key: 'parts', label: '零件管理', path: '/parts' },
  { key: 'bom', label: 'BOM管理', path: '/bom' },
  { key: 'batch', label: '批次追溯', path: '/batch' },
  { key: 'audit', label: '操作记录', path: '/audit' },
  { key: 'backup', label: '备份导出', path: '/backup' },
  { key: 'users', label: '用户管理', path: '/users' },
]
