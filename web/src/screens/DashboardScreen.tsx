/**
 * 工作台：首屏，登录后的默认落点。
 *
 * 四宫格（左列看库存，右列看生产）：
 *   左上 现状预警   —— 当前库存已经无货/低于预警线（实心徽章）
 *   左下 使用中预警 —— 全部在产批次完成后将无货/低于预警线（虚线徽章）
 *   右上 生产计划   —— 待生产/生产中/已暂停的批次与备料结论
 *   右下 操作记录   —— 最近 30 条审计
 *
 * 两类预警**分栏**而不是合成一张表：它们的处理动作不同——现状预警是"去补货"，
 * 使用中预警是"排产时就要备料"。混在一起后用户分不清"现在就该处理"与
 * "这批做完会出问题"。
 *
 * 数据来自五个既有/只读端点，没有新增 /api/dashboard：
 * 工作台必须独立成立，不能要求用户先访问别的屏。
 */
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { isStockAlert, isProjectedAlert, stockView } from '../lib/lowStock'
import { projection as projectionApi, RESERVE_TEXT, type BatchReserve, type PartProjection } from '../lib/projection'
import { tableLabel, actionLabel, shortSummary } from '../lib/auditLabels'
import { Badge, Panel, ScreenError, classifyLoadError, fmtQty, shortTime } from '../components/ui'
import { BATCH_STATUS } from '../lib/batch'

interface Part {
  id: number
  code: string
  name: string
  stock_qty: number
  warn_qty: number
  unit?: string
  status: number
}
interface Product {
  id: number
  code: string
  name: string
  status: number
}
interface Batch {
  id: number
  batch_no: string
  product_name?: string
  product_code?: string
  plan_qty: number
  status: number
  created_at?: string
  customer?: string
}
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

/** 未完成批次：待生产 / 生产中 / 已暂停。 */
function isOpenBatch(status: number): boolean {
  return status === BATCH_STATUS.pending || status === BATCH_STATUS.running || status === BATCH_STATUS.paused
}

const BATCH_STATUS_TEXT: Record<number, string> = {
  [BATCH_STATUS.pending]: '待生产',
  [BATCH_STATUS.running]: '生产中',
  [BATCH_STATUS.paused]: '已暂停',
}

function batchStatusKind(status: number): 'ok' | 'warn' | 'neutral' {
  if (status === BATCH_STATUS.running) return 'ok'
  if (status === BATCH_STATUS.paused) return 'warn'
  return 'neutral'
}

export function DashboardScreen() {
  const [parts, setParts] = useState<Part[] | null>(null)
  const [products, setProducts] = useState<Product[] | null>(null)
  const [batches, setBatches] = useState<Batch[] | null>(null)
  const [logs, setLogs] = useState<AuditLog[] | null>(null)
  const [proj, setProj] = useState<PartProjection[] | null>(null)
  const [reserve, setReserve] = useState<BatchReserve[]>([])
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)

  useEffect(() => {
    let alive = true
    // 任意一块失败不该让整屏变错误页——零件接口 500 时，统计卡与操作记录
    // 仍然有用。五块全失败才算这一屏真的不可用（与零件屏同一策略）。
    let okCount = 0
    let firstErr: unknown = null

    const settle = (p: Promise<unknown>, set: (v: unknown) => void) =>
      p
        .then((v) => {
          if (!alive) return
          okCount += 1
          set(v)
        })
        .catch((err) => {
          if (!alive) return
          if (firstErr === null) firstErr = err
        })

    const jobs: [Promise<unknown>, (v: unknown) => void][] = [
      [api.get<Part[]>('/api/parts'), (v) => setParts(v as Part[])],
      [api.get<Product[]>('/api/products'), (v) => setProducts(v as Product[])],
      [api.get<Batch[]>('/api/batches'), (v) => setBatches(v as Batch[])],
      [api.get<AuditLog[]>('/api/audit/recent?limit=30'), (v) => setLogs(v as AuditLog[])],
      [
        projectionApi.get().then((r) => {
          // 投影是附加信息：即使它失败，也不计入"整屏失败"的判断。
          if (!alive) return
          okCount += 1
          setProj(r.parts)
          setReserve(r.batches)
        }),
        () => undefined,
      ],
    ]
    jobs.forEach(([p, set]) => settle(p, set))

    Promise.allSettled(jobs.map(([p]) => p)).then(() => {
      if (!alive) return
      if (okCount === 0 && firstErr !== null) {
        setError(classifyLoadError(firstErr, '工作台'))
      }
    })

    return () => {
      alive = false
    }
  }, [])

  const partList = parts ?? []
  const projByPart = new Map((proj ?? []).map((p) => [p.part_id, p]))
  const partByID = new Map(partList.map((p) => [p.id, p]))

  // 现状预警：当前无货或低于预警线（含停用件，与之前一致）。
  const currentAlerts = partList.filter((p) => p.status !== 1 || isStockAlert(p))

  // 使用中预警：按全部在产批次完成后的余量判断，停用件不进（不会补货）。
  const projectedAlerts = (proj ?? [])
    .filter((row) => {
      const p = partByID.get(row.part_id)
      if (!p || p.status !== 1) return false
      return isProjectedAlert(stockView(p, row))
    })
    .map((row) => ({ row, part: partByID.get(row.part_id)! }))

  const plan = (batches ?? []).filter((b) => isOpenBatch(b.status))
  const reserveByBatch = new Map(reserve.map((r) => [r.batch_id, r]))

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  const cards = [
    { label: '零件总数', value: partList.length },
    { label: '产品总数', value: (products ?? []).length },
    { label: '在产批次', value: plan.length },
    { label: '库存预警', value: currentAlerts.filter((a) => a.status === 1 && isStockAlert(a)).length },
  ]

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">工作台</h1>
        <div className="ml-auto text-[11.5px] text-[var(--color-fg-subtle)]">
          最后刷新：{new Date().toLocaleTimeString('zh-CN', { hour12: false })}
        </div>
      </div>

      {/* 统计卡 */}
      <div className="grid grid-cols-4 gap-2.5">
        {cards.map((c) => (
          <Panel key={c.label} className="px-3 py-2.5">
            <div className="text-[11.5px] text-[var(--color-fg-muted)]">{c.label}</div>
            <div className="mt-1 text-[22px] font-bold text-[var(--color-primary)] tabular-nums">
              {parts === null && c.label === '零件总数' ? '—' : c.value}
            </div>
          </Panel>
        ))}
      </div>

      <div className="grid min-h-0 flex-1 grid-cols-2 grid-rows-2 gap-2.5 overflow-hidden">
        {/* 左上：现状预警 */}
        <Panel panelId="current-alerts" className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
            <span className="text-[13px] font-semibold">现状预警</span>
            <span className="text-[11.5px] text-[var(--color-fg-subtle)]">{currentAlerts.length} 项</span>
            <span className="ml-auto text-[11px] text-[var(--color-fg-subtle)]">当前库存</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {currentAlerts.length === 0 ? (
              <div className="grid h-full place-items-center text-[12px] text-[var(--color-fg-subtle)]">
                暂无预警
              </div>
            ) : (
              <table className="data-table is-compact">
                <thead>
                  <tr>
                    <th style={{ width: 96 }}>编码</th>
                    <th>名称</th>
                    <th className="num" style={{ width: 64 }}>库存</th>
                    <th className="mid" style={{ width: 68 }}>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {currentAlerts.slice(0, 8).map((p) => {
                    const view = stockView(p, projByPart.get(p.id))
                    return (
                      <tr key={p.id}>
                        <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.code}</td>
                        <td>{p.name}</td>
                        <td className="num">{fmtQty(p.stock_qty)}</td>
                        <td className="mid">
                          <Badge kind={view.tone} title={view.hint}>
                            {view.text}
                          </Badge>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </div>
        </Panel>

        {/* 右上：生产计划 */}
        <Panel panelId="production-plan" className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
            <span className="text-[13px] font-semibold">生产计划</span>
            <span className="text-[11.5px] text-[var(--color-fg-subtle)]">{plan.length} 批在产</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {plan.length === 0 ? (
              <div className="grid h-full place-items-center text-[12px] text-[var(--color-fg-subtle)]">
                当前没有待生产或生产中的批次
              </div>
            ) : (
              <table className="data-table is-compact">
                <thead>
                  <tr>
                    <th style={{ width: 110 }}>批次号</th>
                    <th>产品</th>
                    <th className="num" style={{ width: 62 }}>计划数</th>
                    <th className="mid" style={{ width: 70 }}>状态</th>
                    <th className="mid" style={{ width: 62 }}>备料</th>
                  </tr>
                </thead>
                <tbody>
                  {plan.slice(0, 6).map((b) => {
                    const r = reserveByBatch.get(b.id)
                    return (
                      <tr key={b.id}>
                        <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{b.batch_no}</td>
                        <td>
                          {b.product_name ?? ''}{' '}
                          <span className="text-[var(--color-fg-muted)]">[{b.product_code ?? ''}]</span>
                        </td>
                        <td className="num">{b.plan_qty}</td>
                        <td className="mid">
                          <Badge kind={batchStatusKind(b.status)}>{BATCH_STATUS_TEXT[b.status] ?? '—'}</Badge>
                        </td>
                        <td className="mid">
                          {r ? (
                            <Badge
                              kind={r.level === 'short' ? 'danger' : r.level === 'low' ? 'warn' : 'ok'}
                              title={(r.parts ?? [])
                                .map((p) => `${p.name} [${p.code}]：完成后 ${p.after}`)
                                .join('；') || undefined}
                            >
                              {RESERVE_TEXT[r.level]}
                            </Badge>
                          ) : (
                            <span className="text-[var(--color-fg-subtle)]">—</span>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </div>
        </Panel>

        {/* 左下：使用中预警（在产后） */}
        <Panel panelId="projected-alerts" className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
            <span className="text-[13px] font-semibold">使用中预警</span>
            <span className="text-[11.5px] text-[var(--color-fg-subtle)]">{projectedAlerts.length} 项</span>
            <span className="ml-auto text-[11px] text-[var(--color-fg-subtle)]">在产批次完成后</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {projectedAlerts.length === 0 ? (
              <div className="grid h-full place-items-center text-[12px] text-[var(--color-fg-subtle)]">
                没有正在生产、完成后会跌破预警线的零件
              </div>
            ) : (
              <table className="data-table is-compact">
                <thead>
                  <tr>
                    <th style={{ width: 96 }}>编码</th>
                    <th>名称</th>
                    <th className="num" style={{ width: 64 }}>占用</th>
                    <th className="num" style={{ width: 64 }}>完成后</th>
                    <th className="mid" style={{ width: 68 }}>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {projectedAlerts.slice(0, 8).map(({ row, part }) => {
                    const view = stockView(part, row)
                    return (
                      <tr key={row.part_id}>
                        <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{part.code}</td>
                        <td>{part.name}</td>
                        <td className="num">{fmtQty(row.committed)}</td>
                        <td className="num">{fmtQty(row.after)}</td>
                        <td className="mid">
                          <Badge kind={view.tone} dashed title={view.hint}>
                            {view.text}
                          </Badge>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </div>
        </Panel>

        {/* 右下：最近操作记录 */}
        <Panel panelId="recent-logs" className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
            <span className="text-[13px] font-semibold">最近操作记录</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {(logs ?? []).length === 0 ? (
              <div className="grid h-full place-items-center text-[12px] text-[var(--color-fg-subtle)]">
                暂无操作记录
              </div>
            ) : (
              <table className="data-table is-compact">
                <thead>
                  <tr>
                    <th style={{ width: 84 }}>时间</th>
                    <th className="mid" style={{ width: 68 }}>对象</th>
                    <th className="mid" style={{ width: 76 }}>动作</th>
                    <th>摘要</th>
                    <th className="mid" style={{ width: 76 }}>操作人</th>
                  </tr>
                </thead>
                <tbody>
                  {(logs ?? []).map((l) => (
                    <tr key={l.id}>
                      <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(l.created_at)}</td>
                      <td className="mid">{tableLabel(l.table_name)}</td>
                      <td className="mid">{actionLabel(l.action)}</td>
                      <td
                        style={{
                          fontFamily: 'Consolas, monospace',
                          fontSize: 12,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {shortSummary(l)}
                      </td>
                      <td className="mid">{l.operator ?? '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </Panel>
      </div>
    </>
  )
}
