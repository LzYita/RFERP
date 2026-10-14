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
import { BackupScreen } from './BackupScreen'
import { DashboardScreen } from './DashboardScreen'
import { PartsScreen } from './PartsScreen'
import { ProductsScreen } from './ProductsScreen'
import { BOMScreen } from './BOMScreen'
import { BatchScreen } from './BatchScreen'
import { PlaceholderScreen } from './PlaceholderScreen'
import { SettingsScreen } from './SettingsScreen'
import { StatsScreen } from './StatsScreen'
import { UsersScreen } from './UsersScreen'

/**
 * 每个条目返回的是 **JSX 元素**，不是组件本身。
 *
 * 写成 `dashboard: DashboardScreen` 再由 renderScreen 里 `factory()` 调用，
 * 等于在 App 的渲染过程中直接执行组件函数，hook 会被挂到 App 的 fiber 上。
 * 切屏时挂载的 hook 数量随之变化，React 直接抛 #310（渲染的 hook 比上次多），
 * 整页白屏——而且 TypeScript 不会报错，只有真跑起来才看得见。
 */
/**
 * 工厂拿得到当前用户。
 *
 * 用户管理屏需要它做两个自我守卫（不能停用/删除/降级自己），Fyne 版读的是
 * uth.Current()。Web 侧原本没有任何屏拿得到 me——它只被 AppShell 持有，
 * 屏之间没有上下文。与其再加一层 Context（为一处用法引入新的跨屏依赖），
 * 不如给工厂加一个参数。
 */
export interface ScreenContext {
  meId: number
}

type Factory = (ctx: ScreenContext) => ReactNode

export const REGISTRY: Record<ModuleKey, Factory> = {
  dashboard: () => <DashboardScreen />,
  stats: () => <StatsScreen />,
  parts: () => <PartsScreen />,
  // Round 3 交付主数据 CRUD；Round 4 起交付批次/事务。
  products: () => <ProductsScreen />,
  bom: () => <BOMScreen />,
  batch: () => <BatchScreen />,
  audit: () => <AuditScreen />,
  // Round 5 交付。破坏性操作另有服务端本机闸门（api.WithHostOperations），
  // 界面只是把按钮置灰并说明原因——置灰不是边界，服务端才是不放行的那个。
  backup: () => <BackupScreen />,
  users: (ctx) => <UsersScreen meId={ctx.meId} />,
}

/**
 * 「设置」不是业务模块，因此**不在** NAV / ModuleKey 里：它的入口在侧栏左下角
 * （AppShell.tsx），不参与权限矩阵，也不按读权限过滤。这里显式处理它，
 * 而不是往 REGISTRY 里塞一个 ModuleKey 之外的键——那会让「导航项即模块」
 * 这条不变量失效，往后谁按 REGISTRY 遍历模块就会多出一个假的。
 */
export const SETTINGS_KEY = 'settings'

export function renderScreen(key: string, ctx: ScreenContext): ReactNode {
  if (key === SETTINGS_KEY) return <SettingsScreen />
  const factory = REGISTRY[key as ModuleKey]
  // 兜底：导航项与注册表不一致时（新增模块忘了注册）明说，而不是白屏——
  // 白屏会被当成加载失败或权限问题来排查。Round 7 之前这里返回的是「设置」，
  // 现在设置已交付，兜底只剩这一种用途。
  return factory ? factory(ctx) : <PlaceholderScreen title={key} />
}
