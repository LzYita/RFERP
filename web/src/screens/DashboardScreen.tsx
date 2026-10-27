/**
 * 工作台：首屏，登录后的默认落点。
 *
 * 四宫格（左列看库存，右列看生产）：
 *   左上 现状预警   —— 当前库存已经无货/低于预警线（实心、现在时文案）
 *   左下 未来预警   —— 全部在产批次完成后将无货/低于预警线（虚线、"将"字）
 *   右上 生产计划   —— 待生产/生产中/已暂停的批次与备料结论
 *   右下 操作记录   —— 最近 30 条审计
 *
 * 两类预警**分栏**且各用各的口径，不是同一张表换个颜色：
 *   - 现状预警只用「现在」的库存判断（currentStockView）；
 *   - 未来预警只用投影判断（projectedStockView），每行必带"将"字与虚线。
 * 一个零件可以同时出现在两栏（现在低库存、做完会缺货），但它在这两栏里的文案
 * 必须不同——否则"现状"栏里出现"将缺料"，用户会以为这一栏在说未来。
 *
 * 点击未来预警里的零件会列出正在消耗它的在产批次：那才是"为什么它会缺"的
 * 答案，也是用户下一步要去处理的计划。
 *
 * 数据来自五个既有/只读端点，没有新增 /api/dashboard：
 * 工作台必须独立成立，不能要求用户先访问别的屏。
 */
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { currentStockView, isStockAlert, projectedStockView } from '../lib/lowStock'
import {
  projection as projectionApi,
  RESERVE_TEXT,
  type BatchReserve,
  type PartProjection,
} from '../lib/projection'
import { tableLabel, actionLabel, shortSummary } from '../lib/auditLabels'
import { Badge, Btn, Panel, ScreenError, classifyLoadError, fmtQty, shortTime } from '../components/ui'
import { Modal } from '../components/form'
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
  // 投影失败与"没有占用"必须分开：前者算不出来，后者是算出来没问题。
  const [projFailed, setProjFailed] = useState(false)
  // 点开"未来预警"里的零件时，列出正在消耗它的在产批次。
  const [holderPart, setHolderPart] = useState<{ row: PartProjection; part: Part } | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)

  useEffect(() => {
    let alive = true
    // 核心四块任意一块失败不该让整屏变错误页——零件接口 500 时，统计卡与
    // 操作记录仍然有用。四块全失败才算这一屏真的不可用（与零件屏同一策略）。
    // 投影单独加载：它只影响「未来预警」与「备料」，成功与否都不参与整屏判定。
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
    ]
    jobs.forEach(([p, set]) => settle(p, set))

    // 投影失败必须看得见：这一栏"没有预警"和"算不出来"是两回事，
    // 把后者画成前者会让人以为在产批次都安全。
    projectionApi
      .get()
      .then((r) => {
        if (!alive) return
        setProj(r.parts)
        setReserve(r.batches)
        setProjFailed(false)
      })
      .catch(() => {
        if (alive) setProjFailed(true)
      })

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
  const partByID = new Map(partList.map((p) => [p.id, p]))

  // 现状预警：当前无货或低于预警线（含停用件，与之前一致）。
  const currentAlerts = partList.filter((p) => p.status !== 1 || isStockAlert(p))

  // 未来预警：只看投影，且只保留"将缺料/将预警"两档（停用件不进——不会补货）。
  const futureAlerts = (proj ?? [])
    .flatMap((row) => {
      const p = partByID.get(row.part_id)
      if (!p || p.status !== 1) return []
      const view = projectedStockView(p, row)
      return view ? [{ row, part: p, view }] : []
    })

  const plan = (batches ?? []).filter((b) => isOpenBatch(b.status))
  const reserveByBatch = new Map(reserve.map((r) => [r.batch_id, r]))
  const batchByID = new Map((batches ?? []).map((b) => [b.id, b]))

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
                    // 只报"现在"：这一栏的标题是现状，出现"将缺料"会名不副实。
                    const view = currentStockView(p)
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
                    <th
                      className="mid"
                      style={{ width: 62 }}
                      title={projFailed ? '在产占用数据加载失败，备料暂不可用' : '按全部在产批次完成后推算'}
                    >
                      备料
                    </th>
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
                                .map((p) => `${p.name} [${p.code}]：完成后可用 ${p.after}`)
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

        {/* 左下：未来预警（在产批次完成后） */}
        <Panel panelId="future-alerts" className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
            <span className="text-[13px] font-semibold">未来预警</span>
            <span className="text-[11.5px] text-[var(--color-fg-subtle)]">
              {projFailed || proj === null ? '—' : `${futureAlerts.length} 项`}
            </span>
            <span className="ml-auto text-[11px] text-[var(--color-fg-subtle)]">在产批次完成后</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {projFailed ? (
              <div className="grid h-full place-items-center px-3 text-center text-[12px] text-[var(--color-warn)]">
                在产占用数据加载失败，未来预警暂不可用
              </div>
            ) : proj === null ? (
              <div className="grid h-full place-items-center text-[12px] text-[var(--color-fg-subtle)]">
                正在计算在产占用…
              </div>
            ) : futureAlerts.length === 0 ? (
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
                    <th
                      className="num"
                      style={{ width: 64 }}
                      title="全部在产批次完成后的可用库存"
                    >
                      可用
                    </th>
                    <th className="mid" style={{ width: 68 }}>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {futureAlerts.slice(0, 8).map(({ row, part, view }) => (
                    // 整行可点：点开看"是哪些在产计划在吃这个零件"，
                    // 那才是用户下一步要处理的单据。
                    <tr
                      key={row.part_id}
                      className="cursor-pointer"
                      data-future-part={part.code}
                      onClick={() => setHolderPart({ row, part })}
                    >
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
                  ))}
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

      {holderPart ? (
        <HolderBatchesModal
          part={holderPart.part}
          row={holderPart.row}
          batchByID={batchByID}
          onClose={() => setHolderPart(null)}
        />
      ) : null}
    </>
  )
}

/**
 * 「这个零件会被哪些在产计划吃掉」。
 *
 * 未来预警只给结论，这里给单据：批次号、状态、整批计划数、以及该零件在这一批
 * 里占多少。用户据此决定是补货、改计划还是先完成某一批。
 */
function HolderBatchesModal({
  part,
  row,
  batchByID,
  onClose,
}: {
  part: Part
  row: PartProjection
  batchByID: Map<number, Batch>
  onClose: () => void
}) {
  const holders = [...(row.holders ?? [])].sort((a, b) => b.qty - a.qty)
  return (
    <Modal
      title={`在产占用 · ${part.code}`}
      width={560}
      onClose={onClose}
      footer={
        <Btn primary onClick={onClose}>
          关闭
        </Btn>
      }
    >
      <div className="mb-2 text-[12px] text-[var(--color-fg-muted)]">
        {part.name}：现有 {fmtQty(row.stock)}，在产占用 {fmtQty(row.committed)}，
        可用 {fmtQty(row.after)}
        {part.unit ? `（${part.unit}）` : ''}
      </div>
      <div className="max-h-[280px] overflow-auto rounded-[6px] border border-[var(--color-border-subtle)]">
        <table className="data-table is-narrow is-compact">
          <colgroup>
            <col style={{ width: 112 }} />
            <col style={{ width: 64 }} />
            <col />
            <col style={{ width: 60 }} />
            <col style={{ width: 80 }} />
          </colgroup>
          <thead>
            <tr>
              <th>批次号</th>
              <th className="mid">状态</th>
              <th>产品</th>
              <th className="num">计划数</th>
              <th className="num">本零件占用</th>
            </tr>
          </thead>
          <tbody>
            {holders.map((h) => {
              const b = h.batch_id ? batchByID.get(h.batch_id) : undefined
              return (
                <tr key={h.batch_no}>
                  <td
                    className="tree-inline"
                    style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}
                    title={h.batch_no}
                  >
                    {h.batch_no}
                  </td>
                  <td className="mid">
                    {b ? (
                      <Badge kind={batchStatusKind(b.status)}>{BATCH_STATUS_TEXT[b.status] ?? '—'}</Badge>
                    ) : (
                      <span className="text-[var(--color-fg-subtle)]">—</span>
                    )}
                  </td>
                  <td>{b?.product_name ?? '—'}</td>
                  <td className="num">{b ? b.plan_qty : '—'}</td>
                  <td className="num">{fmtQty(h.qty)}</td>
                </tr>
              )
            })}
            {holders.length === 0 ? (
              <tr>
                <td colSpan={5} className="py-6 text-center text-[12px] text-[var(--color-fg-subtle)]">
                  没有在产批次占用这个零件
                </td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </Modal>
  )
}
