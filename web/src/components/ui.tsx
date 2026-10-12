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
  title,
}: {
  children: ReactNode
  onClick: () => void
  active?: boolean
  disabled?: boolean
  title?: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={[
        'grid h-[22px] min-w-[22px] place-items-center rounded-[4px] border px-1 text-[11.5px] disabled:opacity-40',
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
 * 三件套：总数/页码导航、每页条数选择、跳页输入。
 *
 * 默认 20 条的依据（实测默认窗口 1360x860）：表格可视区 605px、表头 29px、
 * 单行 31px，恰好容纳 18 行。10 条会空出 298px——一大片留白会让界面显得没做完；
 * 20 条填满，是这几个档位里最合适的默认。
 */
export function Pagination({
  total,
  shown,
  page,
  pages,
  pageSize,
  onPage,
  onPageSize,
  unit = '条',
}: {
  total: number
  shown: number
  page: number
  pages: number
  pageSize: number
  onPage: (n: number) => void
  onPageSize: (n: number) => void
  unit?: string
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 border-t border-[var(--color-border)] px-2.5 py-2 text-[11.5px] text-[var(--color-fg-muted)]">
      <span>
        共 {total} {unit} · 本页 {shown} {unit}
      </span>

      {/* 每页条数 */}
      <label className="flex items-center gap-1">
        每页
        <select
          value={pageSize}
          onChange={(e) => {
            // 换档后回到第一页：留在第 5 页而每页从 10 变 30，
            // 用户会突然看到一个不相干的页码。
            onPageSize(Number(e.target.value))
          }}
          aria-label="每页显示条数"
          className="h-[22px] rounded-[4px] border border-[var(--color-border)] bg-[var(--color-surface)] px-1 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
        >
          {[10, 20, 30].map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
        {unit}
      </label>

      {/* 跳页 */}
      {pages > 1 && (
        <label className="flex items-center gap-1">
          跳至
          <input
            type="number"
            min={1}
            max={pages}
            defaultValue={page}
            key={page}
            onKeyDown={(e) => {
              if (e.key !== 'Enter') return
              const n = Number((e.target as HTMLInputElement).value)
              if (!Number.isFinite(n)) return
              // 越界就夹回去，而不是跳到一个空白页
              onPage(Math.min(pages, Math.max(1, Math.round(n))))
            }}
            onBlur={(e) => {
              // 失焦时也归位，避免输入框里停着一个无效数字
              e.target.value = String(page)
            }}
            aria-label="跳转到页码"
            className="h-[22px] w-[46px] rounded-[4px] border border-[var(--color-border)] bg-[var(--color-surface)] px-1 text-center text-[11.5px] outline-none focus:border-[var(--color-primary)]"
          />
          {unit === '条' ? '页' : ''}
        </label>
      )}

      <div className="ml-auto flex items-center gap-1">
        <PageBox onClick={() => onPage(1)} disabled={page === 1} title="第一页">
          «
        </PageBox>
        <PageBox onClick={() => onPage(Math.max(1, page - 1))} disabled={page === 1} title="上一页">
          ‹
        </PageBox>
        {pageWindow(page, pages).map((n, i) => (
          <span key={i} className="contents">
            {n === '…' ? (
              <span className="px-0.5 text-[var(--color-fg-subtle)]">…</span>
            ) : (
              <PageBox key={n} onClick={() => onPage(n as number)} active={n === page}>
                {n}
              </PageBox>
            )}
          </span>
        ))}
        <PageBox onClick={() => onPage(Math.min(pages, page + 1))} disabled={page === pages} title="下一页">
          ›
        </PageBox>
        <PageBox onClick={() => onPage(pages)} disabled={page === pages} title="最后一页">
          »
        </PageBox>
      </div>
    </div>
  )
}

/**
 * 页码窗口：始终显示首末页，当前页两侧各留一页，其余折叠为「…」。
 *
 * 原来固定取 1..5，在 30 条/页、几百条数据时会算出几十页，
 * 用户只能一格格点——这就是要加跳页输入的原因。
 */
function pageWindow(page: number, pages: number): (number | '…')[] {
  if (pages <= 7) return Array.from({ length: pages }, (_, i) => i + 1)
  const out: (number | '…')[] = [1]
  const from = Math.max(2, page - 1)
  const to = Math.min(pages - 1, page + 1)
  if (from > 2) out.push('…')
  for (let i = from; i <= to; i++) out.push(i)
  if (to < pages - 1) out.push('…')
  out.push(pages)
  return out
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