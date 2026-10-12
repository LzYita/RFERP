/**
 * 操作记录（只读）。
 *
 * 对齐 Fyne 的 internal/ui/audit.go。列表数据走 /api/audit/recent?limit=500，
 * 与 Fyne 版 ListRecentAuditLogs(500) 一致。
 *
 * 注意 /api/audit 是**按记录**查的（需 record_id 与 table），不是全局列表——
 * 全局列表只有 /api/audit/recent 这一个端点，筛选只能在已取回的 500 条内做。
 */
import { useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import { tableLabel, actionLabel, shortSummary } from '../lib/auditLabels'
import { Pagination, Panel, ScreenError, classifyLoadError, shortTime } from '../components/ui'

interface AuditLog {
  id: number
  table_name: string
  record_id: number
  action: string
  operator?: string
  new_data?: Record<string, unknown> | null
  old_data?: Record<string, unknown> | null
  created_at: string
}

const DEFAULT_PAGE_SIZE = 20
// 与 Fyne 版一致：一次取 500 条，筛选与分页都在这份快照内完成。
// 服务端没有分页参数，加游标要改 API，超出本轮「不加端点」的范围。
const FETCH_LIMIT = 500

export function AuditScreen() {
  const [logs, setLogs] = useState<AuditLog[] | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE)
  const [q, setQ] = useState('')
  const [table, setTable] = useState('')

  useEffect(() => {
    let alive = true
    api
      .get<AuditLog[]>(`/api/audit/recent?limit=${FETCH_LIMIT}`)
      .then((rows) => {
        if (alive) {
          setLogs(rows)
          setError(null)
        }
      })
      .catch((err) => {
        if (alive) setError(classifyLoadError(err, '操作记录'))
      })
    return () => {
      alive = false
    }
  }, [])

  const tableOptions = useMemo(() => {
    const set = new Set<string>()
    ;(logs ?? []).forEach((l) => set.add(l.table_name))
    return Array.from(set).sort()
  }, [logs])

  const filtered = useMemo(() => {
    let rows = logs ?? []
    if (table) rows = rows.filter((l) => l.table_name === table)
    if (q.trim()) {
      const k = q.trim().toLowerCase()
      rows = rows.filter(
        (l) =>
          (l.operator ?? '').toLowerCase().includes(k) ||
          tableLabel(l.table_name).toLowerCase().includes(k) ||
          actionLabel(l.action).toLowerCase().includes(k) ||
          shortSummary(l).toLowerCase().includes(k),
      )
    }
    return rows
  }, [logs, q, table])

  const pages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const current = Math.min(page, pages)
  const rows = filtered.slice((current - 1) * pageSize, current * pageSize)

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">操作记录</h1>
      </div>

      {/* 筛选区 */}
      <section className="rounded-[8px] border border-[var(--color-border)] bg-[var(--color-surface)] p-2.5">
        <div className="flex flex-wrap items-end gap-2.5">
          <label className="flex flex-col gap-[3px]">
            <span className="text-[11px] text-[var(--color-fg-muted)]">对象</span>
            <select
              value={table}
              onChange={(e) => {
                setTable(e.target.value)
                setPage(1)
              }}
              className="h-[26px] w-[130px] rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            >
              <option value="">全部</option>
              {tableOptions.map((t) => (
                <option key={t} value={t}>
                  {tableLabel(t)}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-[3px]">
            <span className="text-[11px] text-[var(--color-fg-muted)]">操作人 / 摘要</span>
            <input
              value={q}
              onChange={(e) => {
                setQ(e.target.value)
                setPage(1)
              }}
              placeholder="全部"
              className="h-[26px] w-[180px] rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
            />
          </label>
          <button
            type="button"
            onClick={() => {
              setQ('')
              setTable('')
              setPage(1)
            }}
            className="inline-flex h-[26px] items-center rounded-[6px] border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 text-[11.5px] hover:bg-[var(--color-canvas)]"
          >
            重置
          </button>
        </div>
      </section>

      <Panel className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
          <span className="text-[13px] font-semibold">操作记录</span>
          <span className="text-[11.5px] text-[var(--color-fg-subtle)]">
            共 {filtered.length} 条（最近 {FETCH_LIMIT} 条内筛选）
          </span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ width: 96 }}>时间</th>
                <th className="mid" style={{ width: 88 }}>对象</th>
                <th className="mid" style={{ width: 96 }}>动作</th>
                <th>摘要</th>
                <th className="mid" style={{ width: 110 }}>操作人</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((l) => (
                <tr key={l.id}>
                  <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(l.created_at)}</td>
                  <td className="mid">{tableLabel(l.table_name)}</td>
                  <td className="mid">{actionLabel(l.action)}</td>
                  <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                    {shortSummary(l)}
                  </td>
                  <td className="mid">{l.operator ?? '—'}</td>
                </tr>
              ))}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={5} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
                    没有符合条件的记录
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
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
        unit="条"
      />
      </Panel>
    </>
  )
}