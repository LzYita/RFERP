/**
 * 统计分析（只读）。
 *
 * 对齐 Fyne 的 internal/ui/stats.go：
 *   - 三张统计卡：累计入库 / 累计出库 / 产品出库总量
 *   - 每日出入库趋势图（内联 SVG 折线，无图表库）
 *   - 五个 Top 榜：供应商 / 客户 / 零件入库 / 零件出库 / 产品
 *
 * 注意 JSON 键是 PascalCase：usecase.StockStats 及其子结构**没有 json tag**，
 * Go 直接以字段名（Date/In/Out/TopIn/...）序列化。写成 snake_case 会全部取到
 * undefined，图和榜会静默空白——这类错误不会报错，只能靠对齐真实响应发现。
 */
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import { Panel, ScreenError, classifyLoadError, fmtQty } from '../components/ui'

interface StockStats {
  Days: { Date: string; In: number; Out: number }[]
  TopIn: PartStat[]
  TopOut: PartStat[]
  TopSuppliers: SupplierStat[]
  TopCustomers: CustomerStat[]
  TopProducts: ProductStat[]
  TotalIn: number
  TotalOut: number
  ProductOutTotal: number
}
interface PartStat {
  Code: string
  Name: string
  In: number
  Out: number
}
interface SupplierStat {
  Name: string
  In: number
}
interface CustomerStat {
  Name: string
  Out: number
}
interface ProductStat {
  Code: string
  Name: string
  Out: number
}

const DAY_OPTIONS = [
  { v: 7, label: '近 7 天' },
  { v: 30, label: '近 30 天' },
  { v: 90, label: '近 90 天' },
]

export function StatsScreen() {
  const [days, setDays] = useState(30)
  const [stats, setStats] = useState<StockStats | null>(null)
  const [error, setError] = useState<{ kind: 'forbidden' | 'server'; text: string } | null>(null)

  useEffect(() => {
    let alive = true
    api
      .get<StockStats>(`/api/stats?days=${days}`)
      .then((s) => {
        if (alive) {
          setStats(s)
          setError(null)
        }
      })
      .catch((err) => {
        if (alive) setError(classifyLoadError(err, '统计分析'))
      })
    return () => {
      alive = false
    }
  }, [days])

  if (error) return <ScreenError kind={error.kind} text={error.text} />

  const cards = [
    { label: '累计入库', value: stats?.TotalIn ?? 0, color: 'var(--color-ok)' },
    { label: '累计出库', value: stats?.TotalOut ?? 0, color: 'var(--color-warn)' },
    { label: '产品出库总量', value: stats?.ProductOutTotal ?? 0, color: 'var(--color-primary)' },
  ]

  return (
    <>
      <div className="flex items-center gap-2.5">
        <h1 className="text-[15px] font-bold">统计分析</h1>
        <div className="ml-auto flex items-center gap-1.5">
          {DAY_OPTIONS.map((o) => (
            <button
              key={o.v}
              type="button"
              onClick={() => setDays(o.v)}
              className={[
                'h-[26px] rounded-[6px] border px-2.5 text-[11.5px]',
                days === o.v
                  ? 'border-[var(--color-primary)] bg-[var(--color-primary)] font-semibold text-white'
                  : 'border-[var(--color-border)] bg-[var(--color-surface)] hover:bg-[var(--color-canvas)]',
              ].join(' ')}
            >
              {o.label}
            </button>
          ))}
        </div>
      </div>

      {/* 统计卡 */}
      <div className="grid grid-cols-3 gap-2.5">
        {cards.map((c) => (
          <Panel key={c.label} className="px-3 py-2.5">
            <div className="text-[11.5px] text-[var(--color-fg-muted)]">{c.label}</div>
            <div className="mt-1 text-[22px] font-bold tabular-nums" style={{ color: c.color }}>
              {fmtQty(c.value)}
            </div>
          </Panel>
        ))}
      </div>

      {/* 趋势图 */}
      <Panel className="px-3 py-2.5">
        <div className="mb-1.5 flex items-center gap-3">
          <span className="text-[13px] font-semibold">每日出入库</span>
          <LegendDot color="var(--color-ok)" label="入库" />
          <LegendDot color="var(--color-warn)" label="出库" />
        </div>
        <TrendChart days={stats?.Days ?? []} />
      </Panel>

      {/* Top 榜 */}
      <div className="grid min-h-0 flex-1 grid-cols-2 gap-2.5 overflow-auto xl:grid-cols-3">
        <RankPanel title="供应商进货 Top" rows={(stats?.TopSuppliers ?? []).map((s) => [s.Name, s.In])} unit="件" />
        <RankPanel title="客户出货 Top" rows={(stats?.TopCustomers ?? []).map((c) => [c.Name, c.Out])} unit="件" />
        <RankPanel title="零件入库 Top" rows={(stats?.TopIn ?? []).map((p) => [p.Name, p.In])} unit="件" />
        <RankPanel title="零件出库 Top" rows={(stats?.TopOut ?? []).map((p) => [p.Name, p.Out])} unit="件" />
        <RankPanel
          title="产品出库 Top"
          rows={(stats?.TopProducts ?? []).map((p) => [p.Name, p.Out])}
          unit="件"
        />
      </div>
    </>
  )
}

/** 趋势图：内联 SVG 双折线，不引图表库（避免为一个只读图引入几十 KB 依赖）。 */
function TrendChart({ days }: { days: { Date: string; In: number; Out: number }[] }) {
  if (days.length === 0) {
    return (
      <div className="grid h-[150px] place-items-center text-[12px] text-[var(--color-fg-subtle)]">
        该时间范围内暂无出入库数据
      </div>
    )
  }
  const W = 800
  const H = 150
  const pad = { l: 34, r: 8, t: 8, b: 18 }
  const maxV = Math.max(1, ...days.map((d) => Math.max(d.In, d.Out)))
  const x = (i: number) => pad.l + (i / Math.max(1, days.length - 1)) * (W - pad.l - pad.r)
  const y = (v: number) => H - pad.b - (v / maxV) * (H - pad.t - pad.b)
  const path = (key: 'In' | 'Out') => days.map((d, i) => `${i === 0 ? 'M' : 'L'}${x(i)},${y(d[key])}`).join(' ')

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full" style={{ height: 150 }}>
      {/* 网格与 Y 轴刻度 */}
      {[0, 0.5, 1].map((f) => (
        <g key={f}>
          <line
            x1={pad.l}
            x2={W - pad.r}
            y1={y(maxV * f)}
            y2={y(maxV * f)}
            stroke="var(--color-border-subtle)"
            strokeWidth="1"
          />
          <text x={4} y={y(maxV * f) + 3} fontSize="9" fill="var(--color-fg-subtle)">
            {Math.round(maxV * f)}
          </text>
        </g>
      ))}
      <path d={path('In')} fill="none" stroke="var(--color-ok)" strokeWidth="1.6" />
      <path d={path('Out')} fill="none" stroke="var(--color-warn)" strokeWidth="1.6" />
      {/* X 轴只标首尾与中点，避免密集重叠 */}
      {[0, Math.floor(days.length / 2), days.length - 1]
        .filter((i, idx, arr) => i >= 0 && arr.indexOf(i) === idx)
        .map((i) => (
          <text
            key={i}
            x={x(i)}
            y={H - 4}
            fontSize="9"
            textAnchor={i === 0 ? 'start' : i === days.length - 1 ? 'end' : 'middle'}
            fill="var(--color-fg-subtle)"
          >
            {days[i].Date}
          </text>
        ))}
    </svg>
  )
}

function LegendDot({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1 text-[11px] text-[var(--color-fg-muted)]">
      <span className="inline-block h-[7px] w-[7px] rounded-full" style={{ background: color }} />
      {label}
    </span>
  )
}

/** 榜单：用横向条表示量级，比纯数字更容易看出差距。 */
function RankPanel({ title, rows, unit }: { title: string; rows: [string, number][]; unit: string }) {
  const max = Math.max(1, ...rows.map((r) => r[1]))
  return (
    <Panel className="flex flex-col overflow-hidden">
      <div className="border-b border-[var(--color-border)] px-2.5 py-1.5 text-[13px] font-semibold">
        {title}
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {rows.length === 0 ? (
          <div className="grid h-full place-items-center py-6 text-[12px] text-[var(--color-fg-subtle)]">
            暂无数据
          </div>
        ) : (
          <table className="data-table">
            <tbody>
              {rows.map(([name, v], i) => (
                <tr key={name + i}>
                  <td style={{ width: 28, color: 'var(--color-fg-subtle)' }}>{i + 1}</td>
                  <td>{name}</td>
                  <td style={{ width: '38%' }}>
                    <span
                      className="block h-[7px] rounded-full bg-[var(--color-primary)]"
                      style={{ width: `${Math.round((v / max) * 100)}%` }}
                    />
                  </td>
                  <td style={{ width: 72, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
                    {fmtQty(v)}
                    {unit}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </Panel>
  )
}