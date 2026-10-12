import { useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import {
  Badge,
  Btn,
  Field,
  Pagination,
  ScreenError,
  classifyLoadError,
  fmtQty,
  shortTime,
} from '../components/ui'

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

const DEFAULT_PAGE_SIZE = 20

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
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
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
        setError(classifyLoadError(err, '零件'))
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

  const pages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const current = Math.min(page, pages)
  const rows = filtered.slice((current - 1) * pageSize, current * pageSize)

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
                <th className="mid" style={{ width: 34 }}>
                  <input type="checkbox" checked={allChecked} onChange={toggleAll} aria-label="全选本页" />
                </th>
                <th>编码</th>
                <th>名称</th>
                <th>规格</th>
                <th className="num">库存</th>
                <th className="num">预警值</th>
                <th className="mid">状态</th>
                <th>更新时间</th>
                <th className="mid col-fix" style={{ width: 120 }}>
                  操作
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => {
                const low = p.stock_qty <= p.warn_qty
                return (
                  <tr key={p.id} data-selected={selected.has(p.id)}>
                    <td className="mid">
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
                    <td className="num">
                      {fmtQty(p.stock_qty)} {p.unit}
                    </td>
                    {/* 预警值：与库存并排才看得出差多少。藏在详情里的话，
                        用户只会看到"低库存"两个字，不知道差多少、补多少。 */}
                    <td className="num" style={{ color: 'var(--color-fg-muted)' }}>
                      {fmtQty(p.warn_qty)} {p.unit}
                    </td>
                    <td className="mid">
                      <Badge kind={p.status === 1 && !low ? 'ok' : low ? 'warn' : 'neutral'}>
                        {p.status !== 1 ? '停用' : low ? '低库存' : '正常'}
                      </Badge>
                    </td>
                    {/* 去年份：年份由筛选/排序语境承担 */}
                    <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(p.updated_at)}</td>
                    <td className="mid col-fix">
                      <button className="mr-2 text-[var(--color-primary)]">编辑</button>
                      <button className="text-[var(--color-primary)]">更多 ▾</button>
                    </td>
                  </tr>
                )
              })}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={9} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    没有符合条件的零件
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* 分页 */}
        <Pagination
        total={filtered.length}
        shown={rows.length}
        page={current}
        pages={pages}
        pageSize={pageSize}
        onPage={setPage}
        onPageSize={(n) => {
          setPageSize(n)
          setPage(1)
        }}
      />
      </section>
    </>
  )
}
