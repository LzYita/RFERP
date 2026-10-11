import { useEffect, useMemo, useState } from 'react'
import { api, ApiError } from '../lib/api'

interface Part {
  id: number
  code: string
  name: string
  spec?: string
  unit: string
  stock_qty: number
  warn_qty: number
  status: number
  updated_at: string
}

const PAGE_SIZE = 10

/**
 * 零件列表：Round 1 的「一个真实列表屏」关口。
 *
 * 页结构按中后台通行骨架：页头 → 筛选区 → 批量栏（选中时）→ 表格 → 分页。
 * 密度、圆角、阈值全部来自 index.css 的定稿 token，这里不写副本。
 *
 * 列的取舍（§Round 1.1）：
 *  - 单位并入库存（`1,284 个`），少一列
 *  - 更新时间去年份（`10-01 08:12`），省约 60px；年份由筛选语境承担
 * 这两处是「一屏能读完」的关键，比调阈值有效得多。
 */
export function PartsScreen() {
  const [parts, setParts] = useState<Part[] | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [page, setPage] = useState(1)
  const [q, setQ] = useState('')
  const [lowOnly, setLowOnly] = useState(false)

  useEffect(() => {
    let alive = true
    api
      .get<Part[]>('/api/parts')
      .then((rows) => {
        if (alive) {
          setParts(rows)
          setError(null)
        }
      })
      .catch((err) => {
        if (!alive) return
        // 权限拒绝与后端故障要分开：前者是账号问题，后者是系统问题，
        // 混成一句「加载失败」会让用户既不知道该找谁、也不知道该重试。
        if (err instanceof ApiError && err.isForbidden) {
          setError({ kind: 'forbidden', text: '当前账号没有零件管理模块的查看权限' })
        } else {
          setError({ kind: 'server', text: `无法加载零件：${(err as Error).message}` })
        }
      })
    return () => {
      alive = false
    }
  }, [])

  const filtered = useMemo(() => {
    let rows = parts ?? []
    if (q.trim()) {
      const k = q.trim().toLowerCase()
      rows = rows.filter((p) => p.code.toLowerCase().includes(k) || p.name.toLowerCase().includes(k))
    }
    if (lowOnly) rows = rows.filter((p) => p.stock_qty <= p.warn_qty)
    return rows
  }, [parts, q, lowOnly])

  const pages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const current = Math.min(page, pages)
  const rows = filtered.slice((current - 1) * PAGE_SIZE, current * PAGE_SIZE)

  const allChecked = rows.length > 0 && rows.every((p) => selected.has(p.id))

  function toggleAll() {
    setSelected((prev) => {
      const next = new Set(prev)
      if (allChecked) rows.forEach((p) => next.delete(p.id))
      else rows.forEach((p) => next.add(p.id))
      return next
    })
  }

  function toggleOne(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">零件管理</h1>
        <div className="ml-auto flex gap-2">
          <Btn>导出</Btn>
          <Btn primary>+ 新增零件</Btn>
        </div>
      </div>

      {/* 筛选区 */}
      <section className="rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5">
        <div className="flex flex-wrap items-end gap-2.5">
          <Field label="编码 / 名称">
            <input
              value={q}
              onChange={(e) => {
                setQ(e.target.value)
                setPage(1)
              }}
              placeholder="全部"
              className="h-[26px] w-[150px] rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            />
          </Field>
          <Field label="库存">
            <label className="flex h-[26px] items-center gap-1.5 rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px]">
              <input type="checkbox" checked={lowOnly} onChange={(e) => setLowOnly(e.target.checked)} />
              低于阈值
            </label>
          </Field>
          <Btn primary onClick={() => setPage(1)}>
            查询
          </Btn>
          <Btn
            onClick={() => {
              setQ('')
              setLowOnly(false)
              setPage(1)
            }}
          >
            重置
          </Btn>
        </div>
      </section>

      {/* 批量操作栏：选中行才出现（Ant 的 tableAlertRender） */}
      {selected.size > 0 && (
        <div className="flex items-center gap-2.5 rounded-[8px] border border-[var(--color-primary-subtle-border)] bg-[var(--color-primary-subtle)] px-3 py-2 text-[12px]">
          <span>
            已选 <b className="text-[var(--color-primary)]">{selected.size}</b> 项（共 {filtered.length}）
          </span>
          <button type="button" className="text-[var(--color-primary)]" onClick={() => setSelected(new Set())}>
            取消选择
          </button>
          <div className="ml-auto flex gap-2">
            <Btn>批量导出</Btn>
            <Btn primary>批量入库</Btn>
            <Btn danger>批量删除</Btn>
          </div>
        </div>
      )}

      {/* 表格 */}
      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)]">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[13px] font-semibold">零件管理</span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">共 {filtered.length} 项</span>
        </div>

        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ width: 34 }}>
                  <input type="checkbox" checked={allChecked} onChange={toggleAll} aria-label="全选本页" />
                </th>
                <th>编码</th>
                <th>名称</th>
                <th>规格</th>
                <th style={{ textAlign: 'right' }}>库存</th>
                <th>状态</th>
                <th>更新时间</th>
                <th className="col-fix" style={{ width: 120 }}>
                  操作
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => {
                const low = p.stock_qty <= p.warn_qty
                return (
                  <tr key={p.id} data-selected={selected.has(p.id)}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selected.has(p.id)}
                        onChange={() => toggleOne(p.id)}
                        aria-label={`选择 ${p.code}`}
                      />
                    </td>
                    <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.code}</td>
                    <td>{p.name}</td>
                    <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.spec ?? '—'}</td>
                    {/* 单位并入库存，省一列 */}
                    <td style={{ textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
                      {fmtQty(p.stock_qty)} {p.unit}
                    </td>
                    <td>
                      <Badge kind={p.status === 1 && !low ? 'ok' : low ? 'warn' : 'neutral'}>
                        {p.status !== 1 ? '停用' : low ? '低库存' : '正常'}
                      </Badge>
                    </td>
                    {/* 去年份：年份由筛选/排序语境承担 */}
                    <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(p.updated_at)}</td>
                    <td className="col-fix">
                      <button className="mr-2 text-[var(--color-primary)]">编辑</button>
                      <button className="text-[var(--color-primary)]">更多 ▾</button>
                    </td>
                  </tr>
                )
              })}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={8} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    没有符合条件的零件
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* 分页 */}
        <div className="flex items-center gap-2.5 border-t border-[var(--color-border)] px-2.5 py-2 text-[11.5px] text-[var(--color-fg-muted)]">
          <span>
            共 {filtered.length} 条 · 本页 {rows.length} 条
          </span>
          <div className="ml-auto flex items-center gap-1">
            <PageBox onClick={() => setPage(1)} disabled={current === 1}>
              ‹
            </PageBox>
            {Array.from({ length: Math.min(pages, 5) }, (_, i) => i + 1).map((n) => (
              <PageBox key={n} onClick={() => setPage(n)} active={n === current}>
                {n}
              </PageBox>
            ))}
            <PageBox onClick={() => setPage(pages)} disabled={current === pages}>
              ›
            </PageBox>
          </div>
        </div>
      </section>
    </>
  )
}

/**
 * 屏级错误。
 *
 * 权限拒绝不提供「重试」：重试不会改变权限，用户点了只会更困惑。
 * 应引导其联系管理员，而不是让它面对一个转圈的按钮。
 */
function ScreenError({ kind, text }: { kind: 'forbidden' | 'server'; text: string }) {
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

function Badge({ kind, children }: { kind: 'ok' | 'warn' | 'neutral'; children: React.ReactNode }) {
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

function Btn({
  children,
  primary,
  danger,
  onClick,
}: {
  children: React.ReactNode
  primary?: boolean
  danger?: boolean
  onClick?: () => void
}) {
  const cls = [
    'inline-flex h-[26px] items-center rounded-[6px] border px-2.5 text-[11.5px]',
    primary
      ? 'border-[var(--color-primary)] bg-[var(--color-primary)] text-white'
      : danger
        ? 'border-[#efc9c9] text-[var(--color-danger)]'
        : 'border-[var(--color-border)] bg-[var(--color-surface)] hover:bg-[var(--color-canvas)]',
  ].join(' ')
  return (
    <button type="button" className={cls} onClick={onClick}>
      {children}
    </button>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-[3px]">
      <span className="text-[11px] text-[var(--color-fg-muted)]">{label}</span>
      {children}
    </label>
  )
}

function PageBox({
  children,
  onClick,
  active,
  disabled,
}: {
  children: React.ReactNode
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

/** 数量最多两位小数，避免 1.0000000000000002 这类浮点噪声占宽。 */
function fmtQty(n: number): string {
  return Number.isInteger(n) ? String(n) : String(Number(n.toFixed(2)))
}

/** 去掉年份：省约 60px，代价是跨年数据需要靠筛选区分。 */
function shortTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (x: number) => String(x).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
