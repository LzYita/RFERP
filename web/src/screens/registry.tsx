/**
 * 屏幕注册表。
 *
 * App.tsx 原本用 `activeKey === 'parts' ? <PartsScreen/> : <Placeholder/>` 逐个判断。
 * Round 1 只有一个真实屏尚可，Round 2 一次加三张后这种三元会变成无法维护的
 * if/else 链。改为查表：新增屏幕只在这里加一行，App 不用动。
 */
import type { ReactNode } from 'react'
import type { ModuleKey } from '../lib/nav'
import { AuditScreen } from './AuditScreen'
import { DashboardScreen } from './DashboardScreen'
import { PartsScreen } from './PartsScreen'
import { PlaceholderScreen } from './PlaceholderScreen'
import { StatsScreen } from './StatsScreen'

/**
 * 每个条目返回的是 **JSX 元素**，不是组件本身。
 *
 * 写成 `dashboard: DashboardScreen` 再由 renderScreen 里 `factory()` 调用，
 * 等于在 App 的渲染过程中直接执行组件函数，hook 会被挂到 App 的 fiber 上。
 * 切屏时挂载的 hook 数量随之变化，React 直接抛 #310（渲染的 hook 比上次多），
 * 整页白屏——而且 TypeScript 不会报错，只有真跑起来才看得见。
 */
type Factory = () => ReactNode

export const REGISTRY: Record<ModuleKey, Factory> = {
  dashboard: () => <DashboardScreen />,
  stats: () => <StatsScreen />,
  parts: () => <PartsScreen />,
  // Round 3 交付主数据 CRUD；Round 4 起交付批次/事务。
  products: () => <PlaceholderScreen title="产品管理" />,
  bom: () => <PlaceholderScreen title="BOM管理" />,
  batch: () => <PlaceholderScreen title="批次追溯" />,
  audit: () => <AuditScreen />,
  // Round 5 交付；且依赖目录枚举端点替代 nativefiledialog。
  backup: () => <PlaceholderScreen title="备份导出" />,
  // Round 6 交付用户与权限矩阵。
  users: () => <PlaceholderScreen title="用户管理" />,
}

export function renderScreen(key: string): ReactNode {
  const factory = REGISTRY[key as ModuleKey]
  return factory ? factory() : <PlaceholderScreen title="设置" />
}