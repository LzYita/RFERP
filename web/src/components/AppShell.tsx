import type { ReactNode } from 'react'
import { NAV } from '../lib/nav'
import { canRead, canWrite, type ModulePermissions } from '../lib/permissions'
import type { Me } from '../lib/api'

/**
 * 应用外壳：深色侧栏 + 顶栏 + 内容区。
 *
 * 布局参数来自 §Round 1.1 定稿：侧栏固定 166px、内容区左右各 13px，
 * 因此可用表格宽 = 视口 − 192，表格阈值 720px 恰好让 940px 起的窗口
 * 都不出现横向滚动条。改侧栏宽度必须同步复核该算式。
 */
export function AppShell({
  me,
  modules,
  active,
  onNavigate,
  onLogout,
  version,
  children,
}: {
  me: Me
  modules: ModulePermissions | null
  active: string
  onNavigate: (key: string, path: string) => void
  onLogout: () => void
  version: string
  children: ReactNode
}) {
  // 「无」的模块整个入口不显示，而不是置灰。
  const visible = NAV.filter((n) => canRead(modules, n.key))

  return (
    <div className="flex h-full">
      <aside className="flex w-[var(--spacing-sidebar)] shrink-0 flex-col border-r border-[var(--color-sidebar-border)] bg-[var(--color-sidebar)] py-3 pr-2 pl-2 text-[var(--color-sidebar-fg)]">
        <div className="mb-2.5 flex items-center gap-2 px-2 pb-2.5 text-[13px] font-bold whitespace-nowrap">
          <span className="h-5 w-5 shrink-0 rounded-[6px] bg-[var(--color-primary)]" />
          <span>仁风仓库</span>
        </div>

        <nav className="flex flex-col gap-0.5">
          {visible.map((n) => (
            <button
              key={n.key}
              type="button"
              onClick={() => onNavigate(n.key, n.path)}
              aria-current={active === n.key ? 'page' : undefined}
              className={[
                'flex items-center gap-2 rounded-[6px] px-2.5 py-1.5 text-left text-[13px] whitespace-nowrap',
                'hover:bg-[var(--color-sidebar-active)]',
                active === n.key
                  ? 'bg-[var(--color-sidebar-active)] font-semibold text-white'
                  : 'text-[var(--color-sidebar-fg)]',
              ].join(' ')}
            >
              <NavIcon moduleKey={n.key} />
              {n.label}
            </button>
          ))}
        </nav>

        <div className="mt-auto border-t border-[var(--color-sidebar-border)] pt-2">
          <NavLink label="设置" active={active === 'settings'} onClick={() => onNavigate('settings', '/settings')} />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-[42px] shrink-0 items-center gap-2.5 border-b border-[var(--color-border)] bg-[var(--color-surface)] px-[13px]">
          <div className="flex h-[26px] max-w-60 flex-1 items-center rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] text-[var(--color-fg-subtle)]">
            搜索零件、批次、操作记录…
          </div>
          <div className="ml-auto flex items-center gap-2 text-[11.5px] text-[var(--color-fg-muted)]">
            <span>v{version}</span>
            <button
              type="button"
              className="h-[26px] rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] hover:bg-[var(--color-canvas)]"
            >
              刷新
            </button>
            <span className="rounded-[6px] border border-[var(--color-border)] px-2.5 py-1.5">
              {me.display_name || me.username}
              {canWrite(modules, 'users') ? ' · 管理员' : ''}
            </span>
            <button
              type="button"
              onClick={onLogout}
              className="h-[26px] rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] hover:bg-[var(--color-canvas)]"
            >
              退出
            </button>
          </div>
        </header>

        <main className="flex min-h-0 flex-1 flex-col gap-2.5 p-[var(--spacing-content-pad)]">{children}</main>
      </div>
    </div>
  )
}

function NavLink({
  label,
  active,
  onClick,
}: {
  label: string
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? 'page' : undefined}
      className={[
        'flex w-full items-center rounded-[6px] px-2.5 py-1.5 text-left text-[13px] whitespace-nowrap',
        active
          ? 'bg-[var(--color-sidebar-active)] font-semibold text-white'
          : 'text-[var(--color-sidebar-fg)] hover:bg-[var(--color-sidebar-active)]',
      ].join(' ')}
    >
      {label}
    </button>
  )
}

/**
 * 导航图标。
 *
 * 刻意用可辨识的字形而非抽象符号：9 个模块靠图标区分时，
 * 陌生图标必须配 tooltip 才可用（这是排除「图标轨 50px」方案的原因之一）。
 */
function NavIcon({ moduleKey }: { moduleKey: string }) {
  const glyph: Record<string, string> = {
    dashboard: '▦',
    stats: '◔',
    products: '▤',
    parts: '▣',
    bom: '◈',
    batch: '◷',
    audit: '⌸',
    backup: '⇩',
    users: '⚙',
  }
  return (
    <span aria-hidden className="w-[15px] shrink-0 text-center text-[14px] opacity-90">
      {glyph[moduleKey] ?? '•'}
    </span>
  )
}
