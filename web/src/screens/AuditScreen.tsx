/**
 * 操作记录（只读）。
 *
 * 结构与 Fyne 的 internal/ui/audit.go 对齐：**六列**（时间/类型/操作/对象/变更明细/操作人），
 * 点任意数据行打开详情（基本信息 + 字段级变更 + 原始 JSON）。
 *
 * 两处曾经踩过的坑，别踩回去：
 *
 * 1. 列表的「变更明细」必须用 `changeText(auditChanges(l))`，**不是 shortSummary**。
 *    曾把它写成只认 code/name/batch_no 的身份摘要，于是出入库、盘点、状态变更、
 *    整库恢复全变成 `#记录id`——而 old_data/new_data 里本来有完整信息。
 *    shortSummary 是工作台的「内容」列用的，两者不是一回事。
 * 2. 「对象」列是记录身份（[编码] 名称 / 批次号 / 批次#x 零件#y），不是表名。
 *    表名由「类型」列承担；合并会让 BOM、投料这类按 id 关联的记录无法辨认。
 *
 * 数据走 /api/audit/recent?limit=500，与 Fyne 版 ListRecentAuditLogs(500) 一致。
 * 注意 /api/audit 是**按记录**查的（需 record_id 与 table），全局列表只有
 * /api/audit/recent 这一个端点，筛选只能在已取回的 500 条内做。
 */
import { useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import {
  actionLabel,
  auditChanges,
  auditObject,
  changeText,
  tableLabel,
  type AuditData,
  type ChangeItem,
} from '../lib/auditLabels'
import { Modal } from '../components/form'
import {
  Pagination,
  Panel,
  ScreenError,
  classifyLoadError,
  fullTime,
  shortTime,
} from '../components/ui'

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
  const [detail, setDetail] = useState<AuditLog | null>(null)

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

  // 对象下拉的候选：固定表名打底，再并上数据里出现过的表。
  // 只用数据推导的话，某类型不在最近 500 条内时选项会消失，用户没法确认
  // "是这段时间没有，还是这版程序不支持"——旧版是固定按钮，语义更稳。
  const tableOptions = useMemo(() => {
    const known = [
      'products',
      'parts',
      'bom_items',
      'product_batches',
      'batch_trace',
      'batch_skip_parts',
      'database',
    ]
    const set = new Set<string>(known)
    ;(logs ?? []).forEach((l) => set.add(l.table_name))
    return Array.from(set).sort((a, b) => tableLabel(a).localeCompare(tableLabel(b), 'zh'))
  }, [logs])

  const filtered = useMemo(() => {
    let rows = logs ?? []
    if (table) rows = rows.filter((l) => l.table_name === table)
    if (q.trim()) {
      const k = q.trim().toLowerCase()
      // 命中范围＝操作人 + 类型 + 动作 + 对象 + 变更明细全文。
      // 变更明细是用户最可能拿来搜的（"谁改过库存""搜 P-0001"），
      // 只搜身份摘要是搜不到这些的。
      rows = rows.filter(
        (l) =>
          (l.operator ?? '').toLowerCase().includes(k) ||
          tableLabel(l.table_name).toLowerCase().includes(k) ||
          actionLabel(l.action).toLowerCase().includes(k) ||
          auditObject(l).toLowerCase().includes(k) ||
          changeText(auditChanges(l)).toLowerCase().includes(k),
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
            <span className="text-[11px] text-[var(--color-fg-muted)]">操作人 / 对象 / 明细</span>
            <input
              value={q}
              onChange={(e) => {
                setQ(e.target.value)
                setPage(1)
              }}
              placeholder="全部"
              className="h-[26px] w-[190px] rounded-[6px] border border-[var(--color-border)] px-2.5 text-[11.5px] outline-none focus:border-[var(--color-primary)]"
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
          <span className="ml-auto text-[11px] text-[var(--color-fg-subtle)]">
            点击任意行查看详情
          </span>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ width: 96 }}>时间</th>
                <th className="mid" style={{ width: 84 }}>类型</th>
                <th className="mid" style={{ width: 90 }}>操作</th>
                <th style={{ width: 170 }}>对象</th>
                <th style={{ width: 380 }}>变更明细</th>
                <th className="mid" style={{ width: 100 }}>操作人</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((l) => (
                <tr
                  key={l.id}
                  // 旧版是点行弹详情。这里补上键盘可达（旧版刻意禁了键盘，
                  // 但 Web 上不给键盘入口就是无障碍缺口）。
                  tabIndex={0}
                  title="点击查看详情"
                  className="cursor-pointer hover:bg-[var(--color-primary-subtle)]"
                  onClick={() => setDetail(l)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      setDetail(l)
                    }
                  }}
                >
                  <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(l.created_at)}</td>
                  <td className="mid">{tableLabel(l.table_name)}</td>
                  <td className="mid">{actionLabel(l.action)}</td>
                  <td>{auditObject(l)}</td>
                  {/* 换行：内容长度不可控，不折行会把表格横向撑爆 */}
                  <td className="wrap" style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>
                    {changeText(auditChanges(l))}
                  </td>
                  <td className="mid">{l.operator ?? '—'}</td>
                </tr>
              ))}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={6} className="py-8 text-center text-[12px] text-[var(--color-fg-subtle)]">
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

      {detail ? <DetailModal log={detail} onClose={() => setDetail(null)} /> : null}
    </>
  )
}

/**
 * 详情弹窗（对齐 audit.go:767-821）。
 *
 * 三块：基本信息、变更明细（字段级 old → new）、原始数据（缩进 JSON）。
 * 原始数据不是给普通用户看的，但审计场景里"我要确认到底写了什么"必须能核到
 * 未加工的原文——字段标签和格式化都可能骗人。
 */
function DetailModal({ log, onClose }: { log: AuditLog; onClose: () => void }) {
  const changes = auditChanges(log)
  return (
    <Modal title="操作记录详情" onClose={onClose} width={680}>
      <dl className="grid grid-cols-[76px_1fr] gap-x-3 gap-y-1.5 text-[12px]">
        <Field k="时间" v={fullTime(log.created_at)} />
        <Field k="类型" v={tableLabel(log.table_name)} />
        <Field k="操作" v={actionLabel(log.action)} />
        <Field k="对象" v={auditObject(log)} />
        {/* 记录ID：列表里不占列，但排查问题时是唯一的抓手 */}
        <Field k="记录ID" v={String(log.record_id)} />
        <Field k="操作人" v={log.operator || '—'} />
      </dl>

      <h3 className="mt-4 mb-1.5 text-[12px] font-semibold">变更明细</h3>
      {changes.length === 0 ? (
        <p className="text-[12px] text-[var(--color-fg-subtle)]">（无字段变更）</p>
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th style={{ width: 110 }}>字段</th>
              <th>旧值</th>
              <th>新值</th>
            </tr>
          </thead>
          <tbody>
            {changes.map((it: ChangeItem, i) => (
              <tr key={`${it.Field}-${i}`}>
                <td>{it.Field}</td>
                <td style={{ color: 'var(--color-fg-muted)' }}>{it.Old || '—'}</td>
                <td>{it.New || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h3 className="mt-4 mb-1.5 text-[12px] font-semibold">原始数据</h3>
      <RawBlock title="old_data" data={log.old_data} />
      <RawBlock title="new_data" data={log.new_data} />
    </Modal>
  )
}

function Field({ k, v }: { k: string; v: string }) {
  return (
    <>
      <dt className="text-[var(--color-fg-muted)]">{k}</dt>
      <dd>{v}</dd>
    </>
  )
}

function RawBlock({ title, data }: { title: string; data: AuditData }) {
  return (
    <div className="mb-2">
      <div className="mb-1 text-[11px] text-[var(--color-fg-subtle)]">{title}</div>
      <pre className="max-h-[180px] overflow-auto rounded-[6px] border border-[var(--color-border)] bg-[var(--color-canvas)] p-2 text-[11px] leading-[1.5]">
        {data ? JSON.stringify(data, null, 2) : '—'}
      </pre>
    </div>
  )
}
