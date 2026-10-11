/**
 * 屏间共享的 UI 原语。
 *
 * Round 1 把这些组件写在 PartsScreen 文件内部——当时只有一个真实屏，复制一份
 * 比抽公共模块便宜。Round 2 一次加三张屏，若照抄就是同一段代码四份，改一次密度
 * 要改四处。这里抽出，PartsScreen 也改用这里的版本。
 *
 * 所有颜色/圆角走 index.css 的定稿 token，这里不写副本。
 */
import type { ReactNode } from 'react'
import { ApiError } from '../lib/api'

/**
 * 屏级错误。
 *
 * 权限拒绝不提供「重试」：重试不会改变权限，用户点了只会更困惑。
 * 应引导其联系管理员，而不是让它面对一个空转的按钮。
 */
export function ScreenError({ kind, text }: { kind: 'forbidden' | 'server'; text: string }) {
  const denied = kind === 'forbidden'
  return (
    <div className="grid flex-1 place-items-center">
      <div
        role="alert"
        className={[
          'max-w-[420px] rounded-[8px] border px-5 py-4 text-center',
          denied
            ? 'border-[var(--color-warn)]/30 bg-[var(--color-warn-bg)]'
            : 'border-[var(--color-danger)]/30 bg-[var(--color-danger-bg)]',
        ].join(' ')}
      >
        <div
          className={[
            'mb-1.5 text-[14px] font-semibold',
            denied ? 'text-[var(--color-warn)]' : 'text-[var(--color-danger)]',
          ].join(' ')}
        >
          {denied ? '没有访问权限' : '无法加载数据'}
        </div>
        <div className="text-[12px] leading-relaxed text-[var(--color-fg-muted)]">
          {text}
          {denied ? '。请联系管理员调整角色后重新登录。' : '。请稍后重试，或联系管理员。'}
        </div>
      </div>
    </div>
  )
}

/**
 * 把加载错误分成「权限」与「故障」两类。
 *
 * 混成一句「加载失败」会让用户既不知道该找谁、也不知道该重试——这是两件
 * 不同性质的事，必须分开说。
 */
export function classifyLoadError(err: unknown, subject: string): { kind: 'forbidden' | 'server'; text: string } {
  if (err instanceof ApiError && err.isForbidden) {
    return { kind: 'forbidden', text: `当前账号没有${subject}模块的查看权限` }
  }
  return { kind: 'server', text: `无法加载${subject}：${(err as Error).message}` }
}

export function Badge({ kind, children }: { kind: 'ok' | 'warn' | 'neutral'; children: ReactNode }) {
  const map = {
    ok: 'bg-[var(--color-ok-bg)] text-[var(--color-ok)]',
    warn: 'bg-[var(--color-warn-bg)] text-[var(--color-warn)]',
    neutral: 'bg-[var(--color-neutral-bg)] text-[var(--color-neutral)]',
  }
  return (
    <span className={['rounded-[4px] px-[7px] py-px text-[10.5px] font-semibold', map[kind]].join(' ')}>
      {children}
    </span>
  )
}

export function Btn({
  children,
  primary,
  danger,
  disabled,
  onClick,
}: {
  children: ReactNode
  primary?: boolean
  danger?: boolean
  disabled?: boolean
  onClick?: () => void
}) {
  const cls = [
    'inline-flex h-[26px] items-center rounded-[6px] border px-2.5 text-[11.5px] disabled:opacity-40',
    primary
      ? 'border-[var(--color-primary)] bg-[var(--color-primary)] text-white'
      : danger
        ? 'border-[#efc9c9] text-[var(--color-danger)]'
        : 'border-[var(--color-border)] bg-[var(--color-surface)] hover:bg-[var(--color-canvas)]',
  ].join(' ')
  return (
    <button type="button" className={cls} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  )
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-[3px]">
      <span className="text-[11px] text-[var(--color-fg-muted)]">{label}</span>
      {children}
    </label>
  )
}

export function PageBox({
  children,
  onClick,
  active,
  disabled,
}: {
  children: ReactNode
  onClick: () => void
  active?: boolean
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={[
        'grid h-[22px] min-w-[22px] place-items-center rounded-[4px] border text-[11.5px] disabled:opacity-40',
        active
          ? 'border-[var(--color-primary)] bg-[var(--color-primary)] font-semibold text-white'
          : 'border-[var(--color-border)] bg-[var(--color-surface)]',
      ].join(' ')}
    >
      {children}
    </button>
  )
}

/**
 * 分页条。
 *
 * 抽出来是因为三张表都要它，而每处重写一遍页码算法很容易在某张屏上
 * 漏掉「当前页被过滤条件缩小后越界」这个边界——current 已经夹过一次。
 */
export function Pagination({
  total,
  shown,
  page,
  pages,
  onPage,
  unit = '条',
}: {
  total: number
  shown: number
  page: number
  pages: number
  onPage: (n: number) => void
  unit?: string
}) {
  return (
    <div className="flex items-center gap-2.5 border-t border-[var(--color-border)] px-2.5 py-2 text-[11.5px] text-[var(--color-fg-muted)]">
      <span>
        共 {total} {unit} · 本页 {shown} {unit}
      </span>
      <div className="ml-auto flex items-center gap-1">
        <PageBox onClick={() => onPage(1)} disabled={page === 1}>
          ‹
        </PageBox>
        {Array.from({ length: Math.min(pages, 5) }, (_, i) => i + 1).map((n) => (
          <PageBox key={n} onClick={() => onPage(n)} active={n === page}>
            {n}
          </PageBox>
        ))}
        <PageBox onClick={() => onPage(pages)} disabled={page === pages}>
          ›
        </PageBox>
      </div>
    </div>
  )
}

/** 页头 + 右侧动作区，三张屏共用。 */
export function PageHeader({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="flex items-center gap-2.5">
      <h1 className="text-[15px] font-bold">{title}</h1>
      {children ? <div className="ml-auto flex gap-2">{children}</div> : null}
    </div>
  )
}

/** 内容区统一外壳：白底、8px 圆角、边框。 */
export function Panel({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <section
      className={`rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] ${className}`}
    >
      {children}
    </section>
  )
}

/** 数量最多两位小数，避免 1.0000000000000002 这类浮点噪声占宽。 */
export function fmtQty(n: number): string {
  return Number.isInteger(n) ? String(n) : String(Number(n.toFixed(2)))
}

/** 去掉年份：省约 60px，代价是跨年数据需要靠筛选区分。 */
export function shortTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (x: number) => String(x).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}