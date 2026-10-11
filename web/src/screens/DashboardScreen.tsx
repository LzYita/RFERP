/**
 * 工作台：首屏，登录后的默认落点。
 *
 * 对齐 Fyne 的 internal/ui/dashboard.go，三块内容一一对应：
 *   1. 统计卡（零件数 / 产品数 / 批次 / 用户）
 *   2. 经营预警（低库存与停用零件）
 *   3. 最近操作记录（/api/audit/recent?limit=30）
 *
 * 数据来自三个既有端点，**不需要**新增 /api/dashboard：
 * 零件与产品列表已由零件管理屏用同一接口提供，这里再取一次换取首屏自洽——
 * 工作台必须独立成立，不能依赖用户先访问别的屏才有数字。
 */
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { tableLabel, actionLabel } from '../lib/auditLabels'
import { Badge, Panel, ScreenError, classifyLoadError, fmtQty, shortTime } from '../components/ui'

interface Part {
  id: number
  code: string
  name: string
  stock_qty: number
  warn_qty: number
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
}
interface AuditLog {
  id: number
  table_name: string
  action: string
  operator?: string
  created_at: string
}

export function DashboardScreen() {
  const [parts, setParts] = useState<Part[] | null>(null)
  const [products, setProducts] = useState<Product[] | null>(null)
  const [batches, setBatches] = useState<Batch[] | null>(null)
  const [logs, setLogs] = useState<AuditLog[] | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)

useEffect(() => {
    let alive = true
    // 工作台任意一块失败不该让整屏变错误页——零件接口 500 时，统计卡与操作记录
    // 仍然有用。四块全失败才算这一屏真的不可用。
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
  const alerts = partList.filter((p) => p.status !== 1 || p.stock_qty <= p.warn_qty)

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  const cards = [
    { label: '零件总数', value: partList.length },
    { label: '产品总数', value: (products ?? []).length },
    { label: '批次数', value: (batches ?? []).length },
    { label: '低库存预警', value: alerts.filter((a) => a.status === 1 && a.stock_qty <= a.warn_qty).length },
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

      <div className="grid min-h-0 flex-1 grid-cols-2 gap-2.5 overflow-hidden">
        {/* 经营预警 */}
        <Panel className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
            <span className="text-[13px] font-semibold">经营预警</span>
            <span className="text-[11.5px] text-[var(--color-fg-subtle)]">{alerts.length} 项</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {alerts.length === 0 ? (
              <div className="grid h-full place-items-center text-[12px] text-[var(--color-fg-subtle)]">
                暂无预警
              </div>
            ) : (
              <table className="data-table">
                <thead>
                  <tr>
                    <th>编码</th>
                    <th>名称</th>
                    <th style={{ textAlign: 'right' }}>库存</th>
                    <th>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {alerts.slice(0, 8).map((p) => (
                    <tr key={p.id}>
                      <td style={{ fontFamily: 'Consolas, monospace', fontSize: 12 }}>{p.code}</td>
                      <td>{p.name}</td>
                      <td style={{ textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
                        {fmtQty(p.stock_qty)}
                      </td>
                      <td>
                        <Badge kind={p.status !== 1 ? 'neutral' : 'warn'}>
                          {p.status !== 1 ? '停用' : '低库存'}
                        </Badge>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </Panel>

        {/* 最近操作记录 */}
        <Panel className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-2.5 py-1.5">
            <span className="text-[13px] font-semibold">最近操作记录</span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            {(logs ?? []).length === 0 ? (
              <div className="grid h-full place-items-center text-[12px] text-[var(--color-fg-subtle)]">
                暂无操作记录
              </div>
            ) : (
              <table className="data-table">
                <thead>
                  <tr>
                    <th style={{ width: 76 }}>时间</th>
                    <th>对象</th>
                    <th style={{ width: 60 }}>动作</th>
                    <th style={{ width: 76 }}>操作人</th>
                  </tr>
                </thead>
                <tbody>
                  {(logs ?? []).map((l) => (
                    <tr key={l.id}>
                      <td style={{ color: 'var(--color-fg-muted)' }}>{shortTime(l.created_at)}</td>
                      <td>{tableLabel(l.table_name)}</td>
                      <td>{actionLabel(l.action)}</td>
                      <td>{l.operator ?? '—'}</td>
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
